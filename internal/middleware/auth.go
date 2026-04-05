package middleware

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/hitesh07082002/irongate/internal/response"
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

func Auth(authCfg config.AuthConfig) Middleware {
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
			route := GetRouteConfig(req)
			if route == nil {
				response.WriteError(w, req, http.StatusInternalServerError, "route config missing")
				return
			}

			if !route.AuthRequired {
				next.ServeHTTP(w, req)
				return
			}

			if secretValue == "" || !signingMethodMatches(algorithm, jwt.SigningMethodHS256.Alg()) {
				response.WriteError(w, req, http.StatusInternalServerError, authConfigErrorMessage)
				return
			}

			authorization := strings.TrimSpace(req.Header.Get("Authorization"))
			if authorization == "" {
				response.WriteError(w, req, http.StatusUnauthorized, authMissingHeaderMessage)
				return
			}

			tokenString, ok := bearerToken(authorization)
			if !ok {
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
				writeAuthError(w, req, err)
				return
			}
			if token == nil || !token.Valid {
				response.WriteError(w, req, http.StatusUnauthorized, authInvalidTokenMessage)
				return
			}
			if claims.ExpiresAt == nil || claims.IssuedAt == nil || strings.TrimSpace(claims.Subject) == "" || strings.TrimSpace(claims.Role) == "" {
				response.WriteError(w, req, http.StatusUnauthorized, authInvalidTokenMessage)
				return
			}

			req.Header.Set(HeaderUserID, claims.Subject)
			req.Header.Set(HeaderUserRole, claims.Role)
			req.Header.Del("Authorization")
			next.ServeHTTP(w, req)
		})
	}
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
