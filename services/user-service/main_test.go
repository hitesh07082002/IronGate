package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestLoginReturnsSignedHS256JWT(t *testing.T) {
	handler := newHandler("service-secret")

	req := httptest.NewRequest(http.MethodPost, "/users/login", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d with body %s", resp.Code, resp.Body.String())
	}

	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if payload.Token == "" {
		t.Fatal("expected login response to include a token")
	}

	token, err := jwt.Parse(payload.Token, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			t.Fatalf("expected signing method %q, got %q", jwt.SigningMethodHS256.Alg(), token.Method.Alg())
		}
		return []byte("service-secret"), nil
	})
	if err != nil {
		t.Fatalf("parse login token: %v", err)
	}
	if !token.Valid {
		t.Fatal("expected login token to be valid")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatalf("expected map claims, got %T", token.Claims)
	}
	if got := claims["sub"]; got != "u-1" {
		t.Fatalf("expected sub %q, got %#v", "u-1", got)
	}
	if got := claims["role"]; got != "admin" {
		t.Fatalf("expected role %q, got %#v", "admin", got)
	}
	if _, ok := claims["iat"].(float64); !ok {
		t.Fatalf("expected iat claim to be numeric, got %#v", claims["iat"])
	}
	if exp, ok := claims["exp"].(float64); !ok {
		t.Fatalf("expected exp claim to be numeric, got %#v", claims["exp"])
	} else {
		expiresAt := time.Unix(int64(exp), 0)
		if expiresAt.Before(time.Now().Add(50*time.Minute)) || expiresAt.After(time.Now().Add(70*time.Minute)) {
			t.Fatalf("expected exp about one hour ahead, got %s", expiresAt)
		}
	}
}

func TestLoginAllowsBenchmarkClaimOverrides(t *testing.T) {
	t.Setenv("IRONGATE_ALLOW_LOGIN_OVERRIDES", "true")
	handler := newHandler("service-secret")

	req := httptest.NewRequest(http.MethodPost, "/users/login", bytes.NewBufferString(`{"subject":"bench-u-42","role":"user"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d with body %s", resp.Code, resp.Body.String())
	}

	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode login response: %v", err)
	}

	token, err := jwt.Parse(payload.Token, func(token *jwt.Token) (any, error) {
		method, ok := token.Method.(*jwt.SigningMethodHMAC)
		if !ok || method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, errors.New("unexpected signing method")
		}
		return []byte("service-secret"), nil
	})
	if err != nil {
		t.Fatalf("parse login token: %v", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatalf("expected map claims, got %T", token.Claims)
	}
	if got := claims["sub"]; got != "bench-u-42" {
		t.Fatalf("expected overridden sub %q, got %#v", "bench-u-42", got)
	}
	if got := claims["role"]; got != "user" {
		t.Fatalf("expected overridden role %q, got %#v", "user", got)
	}
}

func TestLoginRejectsBenchmarkClaimOverridesWhenDisabled(t *testing.T) {
	handler := newHandler("service-secret")

	req := httptest.NewRequest(http.MethodPost, "/users/login", bytes.NewBufferString(`{"subject":"bench-u-42","role":"user"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "req-disabled")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d with body %s", resp.Code, resp.Body.String())
	}

	var payload struct {
		Error     string `json:"error"`
		Code      int    `json:"code"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload.Error != "invalid login request" {
		t.Fatalf("expected standardized error, got %#v", payload)
	}
	if payload.Code != http.StatusBadRequest {
		t.Fatalf("expected code %d, got %d", http.StatusBadRequest, payload.Code)
	}
	if payload.RequestID != "req-disabled" {
		t.Fatalf("expected request ID %q, got %q", "req-disabled", payload.RequestID)
	}
}

func TestLoginRejectsMalformedOverridePayload(t *testing.T) {
	handler := newHandler("service-secret")

	req := httptest.NewRequest(http.MethodPost, "/users/login", bytes.NewBufferString(`{"subject":`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "req-malformed")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d with body %s", resp.Code, resp.Body.String())
	}

	var payload struct {
		Error     string `json:"error"`
		Code      int    `json:"code"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload.Error != "invalid login request" {
		t.Fatalf("expected standardized error, got %#v", payload)
	}
	if payload.Code != http.StatusBadRequest {
		t.Fatalf("expected code %d, got %d", http.StatusBadRequest, payload.Code)
	}
	if payload.RequestID != "req-malformed" {
		t.Fatalf("expected request ID %q, got %q", "req-malformed", payload.RequestID)
	}
}

func TestLoginFailsClosedWithoutJWTSecret(t *testing.T) {
	handler := newHandler("")

	req := httptest.NewRequest(http.MethodPost, "/users/login", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d with body %s", resp.Code, resp.Body.String())
	}
}

func TestResolveLoginClaimsRejectsInvalidUserRecordClaims(t *testing.T) {
	_, _, err := resolveLoginClaims(nil, map[string]any{"id": 42, "role": "admin"})
	if err == nil {
		t.Fatal("expected invalid id claim to fail")
	}

	_, _, err = resolveLoginClaims(nil, map[string]any{"id": "u-1", "role": ""})
	if err == nil {
		t.Fatal("expected empty role claim to fail")
	}
}

func TestRequiredJWTSecretRejectsMissingValue(t *testing.T) {
	if _, err := requiredJWTSecret("   "); err == nil {
		t.Fatal("expected missing JWT secret to be rejected")
	}
}

func TestRequiredJWTSecretKeepsConfiguredValue(t *testing.T) {
	got, err := requiredJWTSecret("service-secret")
	if err != nil {
		t.Fatalf("expected configured JWT secret to be accepted, got %v", err)
	}
	if got != "service-secret" {
		t.Fatalf("expected JWT secret %q, got %q", "service-secret", got)
	}
}
