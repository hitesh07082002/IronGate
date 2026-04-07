package middleware

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/hitesh07082002/irongate/internal/response"
	"github.com/hitesh07082002/irongate/internal/telemetry"
)

const (
	authSchemeBearer          = "Bearer"
	authConfigErrorMessage    = "jwt auth is not configured"
	authMissingHeaderMessage  = "missing authorization header"
	authMalformedTokenMessage = "malformed token"
	authExpiredTokenMessage   = "token expired"
	authInvalidTokenMessage   = "invalid token"
)

type authClaims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

func Auth(authCfg config.AuthConfig, tracer trace.Tracer) Middleware {
	if tracer == nil {
		tracer = noop.NewTracerProvider().Tracer("irongate.middleware.auth")
	}

	algorithm := strings.TrimSpace(authCfg.JWTAlgorithm)
	secretValue := strings.TrimSpace(authCfg.JWTSecret)
	secret := []byte(secretValue)
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{algorithm}),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			spanCtx, span := tracer.Start(req.Context(), "irongate.middleware.auth")
			defer span.End()
			req = req.WithContext(spanCtx)

			route := GetRouteConfig(req)
			if route == nil {
				logAuthFailure(req, nil, "route config missing")
				span.SetAttributes(attribute.String("auth.outcome", "failed"))
				span.SetStatus(codes.Error, "route config missing")
				response.WriteError(w, req, http.StatusInternalServerError, "route config missing")
				return
			}

			if !route.AuthRequired {
				span.SetAttributes(attribute.String("auth.outcome", "skipped"))
				next.ServeHTTP(w, req)
				return
			}

			if secretValue == "" || !signingMethodMatches(algorithm, jwt.SigningMethodHS256.Alg()) {
				logAuthFailure(req, route, authConfigErrorMessage)
				span.SetAttributes(attribute.String("auth.outcome", "failed"))
				span.SetStatus(codes.Error, authConfigErrorMessage)
				response.WriteError(w, req, http.StatusInternalServerError, authConfigErrorMessage)
				return
			}

			authorization := strings.TrimSpace(req.Header.Get("Authorization"))
			if authorization == "" {
				logAuthFailure(req, route, authMissingHeaderMessage)
				span.SetAttributes(attribute.String("auth.outcome", "failed"))
				span.SetStatus(codes.Error, authMissingHeaderMessage)
				response.WriteError(w, req, http.StatusUnauthorized, authMissingHeaderMessage)
				return
			}

			tokenString, ok := bearerToken(authorization)
			if !ok {
				logAuthFailure(req, route, authMalformedTokenMessage)
				span.SetAttributes(attribute.String("auth.outcome", "failed"))
				span.SetStatus(codes.Error, authMalformedTokenMessage)
				response.WriteError(w, req, http.StatusUnauthorized, authMalformedTokenMessage)
				return
			}

			claims := &authClaims{}
			token, err := parser.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
				if !signingMethodMatches(token.Method.Alg(), algorithm) {
					return nil, jwt.ErrTokenUnverifiable
				}

				return secret, nil
			})
			if err != nil {
				logAuthFailure(req, route, authInvalidTokenMessage)
				span.SetAttributes(attribute.String("auth.outcome", "failed"))
				span.SetStatus(codes.Error, authInvalidTokenMessage)
				writeAuthError(w, req, err)
				return
			}
			if token == nil || !token.Valid {
				logAuthFailure(req, route, authInvalidTokenMessage)
				span.SetAttributes(attribute.String("auth.outcome", "failed"))
				span.SetStatus(codes.Error, authInvalidTokenMessage)
				response.WriteError(w, req, http.StatusUnauthorized, authInvalidTokenMessage)
				return
			}
			if claims.ExpiresAt == nil || claims.IssuedAt == nil || strings.TrimSpace(claims.Subject) == "" || strings.TrimSpace(claims.Role) == "" {
				logAuthFailure(req, route, authInvalidTokenMessage)
				span.SetAttributes(attribute.String("auth.outcome", "failed"))
				span.SetStatus(codes.Error, authInvalidTokenMessage)
				response.WriteError(w, req, http.StatusUnauthorized, authInvalidTokenMessage)
				return
			}

			span.SetAttributes(
				attribute.String("auth.outcome", "passed"),
				attribute.String("auth.user_id", telemetry.HashAttr(claims.Subject)),
				attribute.String("auth.role", claims.Role),
			)
			req.Header.Set(HeaderUserID, claims.Subject)
			req.Header.Set(HeaderUserRole, claims.Role)
			req.Header.Del("Authorization")
			next.ServeHTTP(w, req)
		})
	}
}

func logAuthFailure(req *http.Request, route *config.RouteConfig, reason string) {
	attrs := map[string]any{
		"request_id": response.RequestID(req),
		"trace_id":   telemetry.TraceIDFromContext(req.Context()),
	}
	if route != nil {
		attrs["service"] = route.Service
		attrs["route"] = route.Path
	}
	telemetry.LogGatewayEvent(slog.Default(), slog.LevelWarn, "auth_failed", reason, attrs)
}

func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, authSchemeBearer) || strings.TrimSpace(token) == "" {
		return "", false
	}

	return strings.TrimSpace(token), true
}

func signingMethodMatches(got, want string) bool {
	if len(got) != len(want) {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func writeAuthError(w http.ResponseWriter, req *http.Request, err error) {
	switch {
	case errors.Is(err, jwt.ErrTokenMalformed):
		response.WriteError(w, req, http.StatusUnauthorized, authMalformedTokenMessage)
	case errors.Is(err, jwt.ErrTokenExpired):
		response.WriteError(w, req, http.StatusUnauthorized, authExpiredTokenMessage)
	default:
		response.WriteError(w, req, http.StatusUnauthorized, authInvalidTokenMessage)
	}
}
