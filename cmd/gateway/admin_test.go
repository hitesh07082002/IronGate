package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/hitesh07082002/irongate/internal/middleware"
	"github.com/hitesh07082002/irongate/internal/response"
	"github.com/hitesh07082002/irongate/internal/transport/circuitbreaker"
)

func TestAdminReset_ValidToken(t *testing.T) {
	registry := circuitbreaker.NewRegistry(config.CBConfig{
		FailureThreshold:    1,
		SuccessThreshold:    1,
		Timeout:             time.Minute,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 1,
	}, nil)
	first := registry.Breaker("user-service-1:8081")
	second := registry.Breaker("user-service-2:8081")
	first.RecordFailure()
	second.RecordFailure()

	handler := newAdminHandler("admin-token", func() *circuitbreaker.Registry {
		return registry
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/circuit-breakers/reset", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d with body %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Body.String(); got != `{"reset":true,"targets_cleared":2}` {
		t.Fatalf("unexpected response body %s", got)
	}
	if first.State() != circuitbreaker.StateClosed {
		t.Fatalf("expected first breaker closed, got %s", first.State())
	}
	if second.State() != circuitbreaker.StateClosed {
		t.Fatalf("expected second breaker closed, got %s", second.State())
	}
}

func TestAdminReset_MissingAuth(t *testing.T) {
	registry := circuitbreaker.NewRegistry(config.CBConfig{FailureThreshold: 1}, nil)
	handler := newAdminHandler("admin-token", func() *circuitbreaker.Registry {
		return registry
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/circuit-breakers/reset", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d with body %s", recorder.Code, recorder.Body.String())
	}
	var body response.ErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode admin error response: %v", err)
	}
	if body.Error != "unauthorized" || body.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected error payload %+v", body)
	}
	if body.RequestID == "" {
		t.Fatal("expected request_id in admin error response")
	}
	if got := recorder.Header().Get("X-Request-ID"); got != body.RequestID {
		t.Fatalf("expected response request id %q, got %q", body.RequestID, got)
	}
}

func TestAdminReset_WrongToken(t *testing.T) {
	registry := circuitbreaker.NewRegistry(config.CBConfig{
		FailureThreshold:    1,
		SuccessThreshold:    1,
		Timeout:             time.Minute,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 1,
	}, nil)
	breaker := registry.Breaker("user-service-1:8081")
	breaker.RecordFailure()

	handler := newAdminHandler("admin-token", func() *circuitbreaker.Registry {
		return registry
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/circuit-breakers/reset", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d with body %s", recorder.Code, recorder.Body.String())
	}
	var body response.ErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode admin error response: %v", err)
	}
	if body.RequestID == "" {
		t.Fatal("expected request_id in admin error response")
	}
	if breaker.State() != circuitbreaker.StateOpen {
		t.Fatalf("expected breaker to remain open after unauthorized reset, got %s", breaker.State())
	}
}

func TestAdminReset_RequiresBearerScheme(t *testing.T) {
	handler := newAdminHandler("admin-token", nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/circuit-breakers/reset", nil)
	req.Header.Set("Authorization", "admin-token")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d with body %s", recorder.Code, recorder.Body.String())
	}
}

func TestAdminReset_UnknownPathUsesJSONErrorAndSanitizesHeaders(t *testing.T) {
	handler := newAdminHandler("admin-token", nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/unknown", nil)
	req.Header.Set(middleware.HeaderRequestID, "spoofed-request")
	req.Header.Set(middleware.HeaderUserID, "spoofed-user")
	req.Header.Set(middleware.HeaderUserRole, "spoofed-role")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d with body %s", recorder.Code, recorder.Body.String())
	}
	var body response.ErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode admin 404 response: %v", err)
	}
	if body.Error != "not found" || body.Code != http.StatusNotFound {
		t.Fatalf("unexpected admin 404 payload %+v", body)
	}
	if body.RequestID == "" {
		t.Fatal("expected request_id in admin 404 response")
	}
	if req.Header.Get(middleware.HeaderUserID) != "" || req.Header.Get(middleware.HeaderUserRole) != "" {
		t.Fatal("expected admin handler to strip spoofed identity headers before routing")
	}
	if got := req.Header.Get(middleware.HeaderRequestID); got == "" || got == "spoofed-request" {
		t.Fatalf("expected admin handler to stamp a fresh request id, got %q", got)
	}
}

func TestAdminReset_WrongMethodUsesJSONErrorAndSanitizesHeaders(t *testing.T) {
	handler := newAdminHandler("admin-token", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin/circuit-breakers/reset", nil)
	req.Header.Set(middleware.HeaderRequestID, "spoofed-request")
	req.Header.Set(middleware.HeaderUserID, "spoofed-user")
	req.Header.Set(middleware.HeaderUserRole, "spoofed-role")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d with body %s", recorder.Code, recorder.Body.String())
	}
	var body response.ErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode admin 405 response: %v", err)
	}
	if body.Error != "method not allowed" || body.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unexpected admin 405 payload %+v", body)
	}
	if body.RequestID == "" {
		t.Fatal("expected request_id in admin 405 response")
	}
	if req.Header.Get(middleware.HeaderUserID) != "" || req.Header.Get(middleware.HeaderUserRole) != "" {
		t.Fatal("expected admin handler to strip spoofed identity headers before method checks")
	}
	if got := req.Header.Get(middleware.HeaderRequestID); got == "" || got == "spoofed-request" {
		t.Fatalf("expected admin handler to stamp a fresh request id, got %q", got)
	}
}

func TestAdminReset_AfterReload(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]string{"instance": "admin"})
	}))
	defer upstream.Close()

	cfg := testConfig(routeForServer(t, "/api/users", "/api", "user-service", upstream.URL))
	cfg.CircuitBreaker = config.CBConfig{
		FailureThreshold:    1,
		SuccessThreshold:    1,
		Timeout:             time.Minute,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 1,
	}

	manager, err := newRuntimeManager(cfg, testLogger(), buildHandlerOptions{})
	if err != nil {
		t.Fatalf("build runtime manager: %v", err)
	}

	target := targetHost(t, upstream.URL)
	originalRegistry := manager.Current().CircuitBreaker
	originalBreaker := originalRegistry.Breaker(target)
	if !originalBreaker.Allow() {
		t.Fatal("expected closed breaker to allow request")
	}
	originalBreaker.RecordFailure()
	if originalBreaker.State() != circuitbreaker.StateOpen {
		t.Fatalf("expected original breaker open, got %s", originalBreaker.State())
	}

	reloaded := testConfig(routeForServer(t, "/api/users", "/api", "user-service", upstream.URL))
	reloaded.CircuitBreaker = cfg.CircuitBreaker
	reloaded.CircuitBreaker.Timeout = 2 * time.Minute
	if err := manager.Reload(reloaded); err != nil {
		t.Fatalf("reload runtime manager: %v", err)
	}

	currentRegistry := manager.Current().CircuitBreaker
	if currentRegistry == originalRegistry {
		t.Fatal("expected reload to swap to a cloned circuit breaker registry")
	}
	if currentRegistry.Breaker(target).State() != circuitbreaker.StateOpen {
		t.Fatalf("expected cloned breaker to preserve open state, got %s", currentRegistry.Breaker(target).State())
	}

	handler := newAdminHandler("admin-token", func() *circuitbreaker.Registry {
		snapshot := manager.Current()
		if snapshot == nil {
			return nil
		}

		return snapshot.CircuitBreaker
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/circuit-breakers/reset", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d with body %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Body.String(); got != `{"reset":true,"targets_cleared":1}` {
		t.Fatalf("unexpected response body %s", got)
	}
	if currentRegistry.Breaker(target).State() != circuitbreaker.StateClosed {
		t.Fatalf("expected current breaker closed after reset, got %s", currentRegistry.Breaker(target).State())
	}
}

func TestAdminBearerToken(t *testing.T) {
	testCases := []struct {
		name   string
		header string
		want   string
		ok     bool
	}{
		{name: "valid bearer", header: "Bearer admin-token", want: "admin-token", ok: true},
		{name: "missing scheme", header: "admin-token", ok: false},
		{name: "wrong scheme case", header: "bearer admin-token", ok: false},
		{name: "missing token", header: "Bearer ", ok: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := adminBearerToken(testCase.header)
			if ok != testCase.ok {
				t.Fatalf("expected ok=%t, got %t", testCase.ok, ok)
			}
			if got != testCase.want {
				t.Fatalf("expected token %q, got %q", testCase.want, got)
			}
		})
	}
}

func TestAdminTokenMatches(t *testing.T) {
	if !adminTokenMatches("admin-token", "admin-token") {
		t.Fatal("expected identical admin tokens to match")
	}
	if adminTokenMatches("short", "much-longer-admin-token") {
		t.Fatal("expected mismatched admin tokens to fail")
	}
	if adminTokenMatches("admin-token", "") {
		t.Fatal("expected empty configured token to fail")
	}
}

func TestResolveAdminAddr(t *testing.T) {
	t.Setenv(adminAddrEnvVar, "")
	if got := resolveAdminAddr(); got != defaultAdminAddr {
		t.Fatalf("expected default admin addr %q, got %q", defaultAdminAddr, got)
	}

	t.Setenv(adminAddrEnvVar, ":19090")
	if got := resolveAdminAddr(); got != ":19090" {
		t.Fatalf("expected configured admin addr, got %q", got)
	}
}

func TestResolveConfigPath(t *testing.T) {
	t.Setenv("IRONGATE_CONFIG", "")
	t.Setenv("GATEWAY_CONFIG", "")
	if got := resolveConfigPath("configs/flag.yaml"); got != "configs/flag.yaml" {
		t.Fatalf("expected flag config path, got %q", got)
	}

	t.Setenv("IRONGATE_CONFIG", "configs/irongate.yaml")
	if got := resolveConfigPath(""); got != "configs/irongate.yaml" {
		t.Fatalf("expected IRONGATE_CONFIG path, got %q", got)
	}

	t.Setenv("IRONGATE_CONFIG", "")
	t.Setenv("GATEWAY_CONFIG", "configs/gateway-env.yaml")
	if got := resolveConfigPath(""); got != "configs/gateway-env.yaml" {
		t.Fatalf("expected GATEWAY_CONFIG path, got %q", got)
	}

	t.Setenv("GATEWAY_CONFIG", "")
	if got := resolveConfigPath(""); got != "configs/gateway.yaml" {
		t.Fatalf("expected default config path, got %q", got)
	}
}

func TestShutdownTimeoutUsesFallbackWhenUnset(t *testing.T) {
	if got := shutdownTimeout(0); got != fallbackShutdownTimeout {
		t.Fatalf("expected fallback shutdown timeout %s, got %s", fallbackShutdownTimeout, got)
	}
}

func TestServeAsync_ForwardsFatalErrors(t *testing.T) {
	errs := make(chan serverError, 1)
	server := &http.Server{
		Addr:    ":-1",
		Handler: http.NewServeMux(),
	}

	serveAsync("admin", server, errs)

	select {
	case err := <-errs:
		if err.name != "admin" {
			t.Fatalf("expected admin server name, got %q", err.name)
		}
		if err.err == nil {
			t.Fatal("expected fatal admin server error")
		}
	case <-time.After(time.Second):
		t.Fatal("expected serveAsync to report a fatal server error")
	}
}
