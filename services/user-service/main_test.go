package main

import (
	"encoding/json"
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

func TestLoginFailsClosedWithoutJWTSecret(t *testing.T) {
	handler := newHandler("")

	req := httptest.NewRequest(http.MethodPost, "/users/login", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d with body %s", resp.Code, resp.Body.String())
	}
}
