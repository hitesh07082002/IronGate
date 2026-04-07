package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/hitesh07082002/irongate/internal/telemetry"
)

func TestAuthRejectsInvalidProtectedRequests(t *testing.T) {
	now := time.Now()
	sharedSecret := "test-secret"

	testCases := []struct {
		name          string
		authorization string
		wantStatus    int
		wantError     string
	}{
		{
			name:       "missing header",
			wantStatus: http.StatusUnauthorized,
			wantError:  authMissingHeaderMessage,
		},
		{
			name:          "malformed bearer token",
			authorization: "Bearer not-a-jwt",
			wantStatus:    http.StatusUnauthorized,
			wantError:     authMalformedTokenMessage,
		},
		{
			name: "wrong algorithm",
			authorization: bearerAuthorization(t, sharedSecret, jwt.SigningMethodHS384, jwt.MapClaims{
				"sub":  "u-1",
				"role": "admin",
				"iat":  now.Unix(),
				"exp":  now.Add(time.Hour).Unix(),
			}),
			wantStatus: http.StatusUnauthorized,
			wantError:  authInvalidTokenMessage,
		},
		{
			name: "invalid signature",
			authorization: bearerAuthorization(t, "different-secret", jwt.SigningMethodHS256, jwt.MapClaims{
				"sub":  "u-1",
				"role": "admin",
				"iat":  now.Unix(),
				"exp":  now.Add(time.Hour).Unix(),
			}),
			wantStatus: http.StatusUnauthorized,
			wantError:  authInvalidTokenMessage,
		},
		{
			name: "expired token",
			authorization: bearerAuthorization(t, sharedSecret, jwt.SigningMethodHS256, jwt.MapClaims{
				"sub":  "u-1",
				"role": "admin",
				"iat":  now.Add(-2 * time.Hour).Unix(),
				"exp":  now.Add(-time.Minute).Unix(),
			}),
			wantStatus: http.StatusUnauthorized,
			wantError:  authExpiredTokenMessage,
		},
		{
			name: "future iat",
			authorization: bearerAuthorization(t, sharedSecret, jwt.SigningMethodHS256, jwt.MapClaims{
				"sub":  "u-1",
				"role": "admin",
				"iat":  now.Add(time.Minute).Unix(),
				"exp":  now.Add(time.Hour).Unix(),
			}),
			wantStatus: http.StatusUnauthorized,
			wantError:  authInvalidTokenMessage,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			handler := Auth(config.AuthConfig{
				JWTSecret:    sharedSecret,
				JWTAlgorithm: "HS256",
			}, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				t.Fatal("expected auth middleware to block request")
			}))

			req := protectedRequest(t, testCase.authorization)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)

			if recorder.Code != testCase.wantStatus {
				t.Fatalf("expected %d, got %d", testCase.wantStatus, recorder.Code)
			}

			var payload struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode auth error response: %v", err)
			}
			if payload.Error != testCase.wantError {
				t.Fatalf("expected error %q, got %q", testCase.wantError, payload.Error)
			}
		})
	}
}

func TestAuthPublicRoutesRemainPublic(t *testing.T) {
	nextCalled := false
	handler := Auth(config.AuthConfig{
		JWTSecret:    "test-secret",
		JWTAlgorithm: "HS256",
	}, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req = req.WithContext(context.WithValue(req.Context(), RouteConfigKey, &config.RouteConfig{
		Path:         "/health",
		Service:      "gateway-internal",
		AuthRequired: false,
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if !nextCalled {
		t.Fatal("expected public route to bypass auth middleware")
	}
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", recorder.Code)
	}
}

func TestAuthRejectsWhitespaceOnlySecret(t *testing.T) {
	handler := Auth(config.AuthConfig{
		JWTSecret:    "   ",
		JWTAlgorithm: "HS256",
	}, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("expected whitespace-only secret to fail closed")
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, protectedRequest(t, bearerAuthorization(t, "test-secret", jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  "u-42",
		"role": "admin",
		"iat":  time.Now().Add(-time.Minute).Unix(),
		"exp":  time.Now().Add(time.Hour).Unix(),
	})))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", recorder.Code)
	}
}

func TestAuthValidTokenInjectsIdentityHeaders(t *testing.T) {
	token := bearerAuthorization(t, "test-secret", jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  "u-42",
		"role": "admin",
		"iat":  time.Now().Add(-time.Minute).Unix(),
		"exp":  time.Now().Add(time.Hour).Unix(),
	})

	handler := Auth(config.AuthConfig{
		JWTSecret:    "test-secret",
		JWTAlgorithm: "HS256",
	}, nil)(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if got := req.Header.Get(HeaderUserID); got != "u-42" {
			t.Fatalf("expected X-User-ID %q, got %q", "u-42", got)
		}
		if got := req.Header.Get(HeaderUserRole); got != "admin" {
			t.Fatalf("expected X-User-Role %q, got %q", "admin", got)
		}
		if got := req.Header.Get("Authorization"); got != "" {
			t.Fatalf("expected Authorization header to be stripped before upstream, got %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, protectedRequest(t, token))

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", recorder.Code)
	}
}

func TestAuthSpanRecordsPassedAttributes(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	token := bearerAuthorization(t, "test-secret", jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  "u-42",
		"role": "admin",
		"iat":  time.Now().Add(-time.Minute).Unix(),
		"exp":  time.Now().Add(time.Hour).Unix(),
	})

	handler := Auth(config.AuthConfig{
		JWTSecret:    "test-secret",
		JWTAlgorithm: "HS256",
	}, tp.Tracer("irongate.middleware.auth"))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, protectedRequest(t, token))

	span := findEndedSpanByName(t, recorder.Ended(), "irongate.middleware.auth")
	if got := spanAttribute(span.Attributes(), "auth.outcome"); got != "passed" {
		t.Fatalf("expected auth.outcome passed, got %v", got)
	}
	if got := spanAttribute(span.Attributes(), "auth.user_id"); got != telemetry.HashAttr("u-42") {
		t.Fatalf("expected hashed auth.user_id, got %v", got)
	}
	if got := spanAttribute(span.Attributes(), "auth.role"); got != "admin" {
		t.Fatalf("expected auth.role admin, got %v", got)
	}
}

func TestAuthSpanRecordsFailureStatus(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	handler := Auth(config.AuthConfig{
		JWTSecret:    "test-secret",
		JWTAlgorithm: "HS256",
	}, tp.Tracer("irongate.middleware.auth"))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("expected auth middleware to reject request")
	}))

	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, protectedRequest(t, ""))

	span := findEndedSpanByName(t, recorder.Ended(), "irongate.middleware.auth")
	if got := spanAttribute(span.Attributes(), "auth.outcome"); got != "failed" {
		t.Fatalf("expected auth.outcome failed, got %v", got)
	}
	if span.Status().Code != codes.Error {
		t.Fatalf("expected auth span status error, got %s", span.Status().Code)
	}
}

func protectedRequest(t *testing.T, authorization string) *http.Request {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}

	return req.WithContext(context.WithValue(req.Context(), RouteConfigKey, &config.RouteConfig{
		Path:         "/api/users",
		Service:      "user-service",
		AuthRequired: true,
	}))
}

func bearerAuthorization(t *testing.T, secret string, method jwt.SigningMethod, claims jwt.MapClaims) string {
	t.Helper()

	token := jwt.NewWithClaims(method, claims)
	signedToken, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	return "Bearer " + signedToken
}
