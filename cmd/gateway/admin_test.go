package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hitesh07082002/irongate/internal/config"
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
	if got := recorder.Body.String(); got != `{"error":"unauthorized","code":401}` {
		t.Fatalf("unexpected response body %s", got)
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
	if breaker.State() != circuitbreaker.StateOpen {
		t.Fatalf("expected breaker to remain open after unauthorized reset, got %s", breaker.State())
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
