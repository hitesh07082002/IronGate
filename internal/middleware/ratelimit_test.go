package middleware

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/hitesh07082002/irongate/internal/ratelimit"
)

type stubRateLimitStore struct {
	decision       ratelimit.Decision
	err            error
	request        ratelimit.Request
	waitForContext bool
}

func (s *stubRateLimitStore) Allow(ctx context.Context, request ratelimit.Request) (ratelimit.Decision, error) {
	s.request = request
	if s.waitForContext {
		<-ctx.Done()
		return ratelimit.Decision{}, ctx.Err()
	}
	return s.decision, s.err
}

func TestRateLimiterFailsOpenWhenStoreIsUnavailable(t *testing.T) {
	store := &stubRateLimitStore{err: errors.New("redis unavailable")}
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, nil))
	nextCalled := false

	handler := RateLimiter(store, logger, RateLimiterOptions{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := rateLimitedRequest(t)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if !nextCalled {
		t.Fatal("expected fail-open rate limiter to call next handler")
	}
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", recorder.Code)
	}
	for _, header := range []string{
		HeaderRateLimitLimit,
		HeaderRateLimitRemaining,
		HeaderRateLimitReset,
		HeaderRetryAfter,
	} {
		if got := recorder.Header().Get(header); got != "" {
			t.Fatalf("expected %s omitted on fail-open, got %q", header, got)
		}
	}
	logOutput := logBuffer.String()
	if !strings.Contains(logOutput, "rate limit store unavailable; allowing request") {
		t.Fatalf("expected warning log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "bucket_kind=ip") {
		t.Fatalf("expected bucket kind in warning log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "bucket_key_hash=") {
		t.Fatalf("expected redacted bucket key hash in warning log, got %q", logOutput)
	}
	if strings.Contains(logOutput, "client_key=") || strings.Contains(logOutput, "ip:127.0.0.1") {
		t.Fatalf("expected raw client key redacted from warning log, got %q", logOutput)
	}
}

func TestRateLimiterRejectsInvalidRouteConfig(t *testing.T) {
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, nil))
	nextCalled := false

	handler := RateLimiter(&stubRateLimitStore{}, logger, RateLimiterOptions{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := rateLimitedRequest(t)
	route := GetRouteConfig(req)
	route.RateLimit.Requests = 0

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if nextCalled {
		t.Fatal("expected invalid rate limit config to fail closed before next handler")
	}
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", recorder.Code)
	}
	if !strings.Contains(logBuffer.String(), "rate limit configuration invalid; rejecting request") {
		t.Fatalf("expected error log, got %q", logBuffer.String())
	}
}

func TestRateLimiterFailsOpenQuicklyWhenStoreStalls(t *testing.T) {
	store := &stubRateLimitStore{waitForContext: true}
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, nil))
	nextCalled := false

	handler := RateLimiter(store, logger, RateLimiterOptions{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := rateLimitedRequest(t)
	recorder := httptest.NewRecorder()

	start := time.Now()
	handler.ServeHTTP(recorder, req)
	elapsed := time.Since(start)

	if !nextCalled {
		t.Fatal("expected stalled rate limiter to fail open and call next handler")
	}
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", recorder.Code)
	}
	if elapsed >= rateLimitStoreTimeout*3 {
		t.Fatalf("expected stalled store to time out quickly, took %s", elapsed)
	}
	if !strings.Contains(logBuffer.String(), "rate limit store unavailable; allowing request") {
		t.Fatalf("expected warning log, got %q", logBuffer.String())
	}
}

func TestRateLimiterFailsOpenWhenStoreIsNotConfigured(t *testing.T) {
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, nil))
	nextCalled := false

	handler := RateLimiter(nil, logger, RateLimiterOptions{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := rateLimitedRequest(t)
	req.Header.Set(HeaderUserID, "u-42")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if !nextCalled {
		t.Fatal("expected nil store to fail open and call next handler")
	}
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", recorder.Code)
	}
	logOutput := logBuffer.String()
	if !strings.Contains(logOutput, "store not configured") {
		t.Fatalf("expected store-not-configured reason in warning log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "bucket_kind=user") {
		t.Fatalf("expected user bucket kind in warning log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "bucket_key_hash=") {
		t.Fatalf("expected redacted bucket key hash in warning log, got %q", logOutput)
	}
	if strings.Contains(logOutput, "user:u-42") || strings.Contains(logOutput, "u-42") {
		t.Fatalf("expected raw user identifier redacted from warning log, got %q", logOutput)
	}
}

func TestRateLimiterUsesAuthenticatedUserIDForKeying(t *testing.T) {
	store := &stubRateLimitStore{
		decision: ratelimit.Decision{
			Allowed:   true,
			Remaining: 9,
			ResetAt:   time.Now().Add(time.Minute),
		},
	}

	handler := RateLimiter(store, testRateLimitLogger(), RateLimiterOptions{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := rateLimitedRequest(t)
	req.Header.Set(HeaderUserID, "u-42")
	req.Header.Set("X-Forwarded-For", "198.51.100.10")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", recorder.Code)
	}
	if got := store.request.Key; got != ratelimit.Key("user:u-42", "/api/orders") {
		t.Fatalf("expected user-keyed rate limit key, got %q", got)
	}
}

func TestRateLimitClientKeyIgnoresUntrustedForwardedFor(t *testing.T) {
	req := rateLimitedRequest(t)
	req.Header.Set("X-Forwarded-For", "198.51.100.10")

	if got := rateLimitClientKey(req, nil); got != "ip:127.0.0.1" {
		t.Fatalf("expected untrusted forwarded-for ignored, got %q", got)
	}
}

func TestRateLimitClientKeyHonorsTrustedProxyForwardedFor(t *testing.T) {
	req := rateLimitedRequest(t)
	req.Header.Set("X-Forwarded-For", "198.51.100.10, 203.0.113.8")

	trustedProxy := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	if got := rateLimitClientKey(req, trustedProxy); got != "ip:203.0.113.8" {
		t.Fatalf("expected first untrusted forwarded hop honored, got %q", got)
	}
}

func TestRateLimitClientKeyTraversesTrustedProxyChain(t *testing.T) {
	req := rateLimitedRequest(t)
	req.Header.Set("X-Forwarded-For", "198.51.100.10, 10.0.0.2")

	trustedProxy := []netip.Prefix{
		netip.MustParsePrefix("127.0.0.1/32"),
		netip.MustParsePrefix("10.0.0.0/8"),
	}
	if got := rateLimitClientKey(req, trustedProxy); got != "ip:198.51.100.10" {
		t.Fatalf("expected trusted proxy chain to resolve original client, got %q", got)
	}
}

func TestRateLimitClientKeyUsesTrustedEdgeWhenEarlierForwardedHopIsMalformed(t *testing.T) {
	req := rateLimitedRequest(t)
	req.Header.Set("X-Forwarded-For", "garbage, 203.0.113.8")

	trustedProxy := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	if got := rateLimitClientKey(req, trustedProxy); got != "ip:203.0.113.8" {
		t.Fatalf("expected last trusted-edge client preserved, got %q", got)
	}
}

func rateLimitedRequest(t *testing.T) *http.Request {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set(HeaderRequestID, "req-1")

	return req.WithContext(context.WithValue(req.Context(), RouteConfigKey, &config.RouteConfig{
		Path:    "/api/orders",
		Service: "order-service",
		RateLimit: &config.RateLimitConfig{
			Requests: 10,
			Window:   time.Minute,
			Strategy: "sliding_window",
		},
	}))
}

func testRateLimitLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}
