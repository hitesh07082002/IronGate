package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/hitesh07082002/irongate/internal/ratelimit"
)

type stubStore struct{}

func (stubStore) Allow(_ context.Context, _ ratelimit.Request) (ratelimit.Decision, error) {
	return ratelimit.Decision{}, nil
}

func TestNextCircuitBreakerRegistry_ReplacesCollectorOnConfigChange(t *testing.T) {
	registerCalls := 0
	unregisterCalls := 0
	initial := nextCircuitBreakerRegistry(
		config.CBConfig{FailureThreshold: 1},
		nil,
		nil,
		func(prometheus.Collector) error {
			registerCalls++
			return nil
		},
		func(prometheus.Collector) bool {
			t.Fatal("unexpected unregister on initial registry")
			return false
		},
	)

	snapshot := &Snapshot{
		Config: &config.Config{
			CircuitBreaker: config.CBConfig{FailureThreshold: 1},
		},
		CircuitBreaker: initial,
	}

	reloaded := nextCircuitBreakerRegistry(
		config.CBConfig{FailureThreshold: 2},
		nil,
		snapshot,
		func(prometheus.Collector) error {
			registerCalls++
			return nil
		},
		func(collector prometheus.Collector) bool {
			unregisterCalls++
			return collector == initial.Collector()
		},
	)

	if registerCalls != 2 {
		t.Fatalf("expected two register calls, got %d", registerCalls)
	}
	if unregisterCalls != 1 {
		t.Fatalf("expected one unregister call, got %d", unregisterCalls)
	}
	if reloaded == initial {
		t.Fatal("expected config change to build a new registry")
	}
	if reloaded.Collector() == initial.Collector() {
		t.Fatal("expected reloaded registry to use a fresh collector")
	}
}

func TestDefaultRateLimitStoreFactory(t *testing.T) {
	if got := defaultRateLimitStoreFactory(nil, nil); got != nil {
		t.Fatalf("expected nil store for nil config, got %#v", got)
	}

	cfg := &config.Config{}
	if got := defaultRateLimitStoreFactory(cfg, nil); got != nil {
		t.Fatalf("expected nil store when no routes are rate limited, got %#v", got)
	}

	store := stubStore{}
	cfg.Routes = []config.RouteConfig{{
		Path: "/api/orders",
		RateLimit: &config.RateLimitConfig{
			Requests: 5,
			Window:   time.Minute,
		},
	}}
	cfg.Redis.Address = "redis:6379"
	previous := &Snapshot{
		Config:         cfg.Clone(),
		RateLimitStore: store,
	}
	if got := defaultRateLimitStoreFactory(cfg, previous); got != previous.RateLimitStore {
		t.Fatal("expected matching redis config to reuse previous rate-limit store")
	}
}

func TestValidateStartupOnlyServerConfig(t *testing.T) {
	startup := config.ServerConfig{Port: 8080, ReadTimeout: time.Second, WriteTimeout: 2 * time.Second}
	if err := validateStartupOnlyServerConfig(startup, startup); err != nil {
		t.Fatalf("expected unchanged startup config to validate, got %v", err)
	}

	next := startup
	next.Port = 9090
	if err := validateStartupOnlyServerConfig(startup, next); err == nil {
		t.Fatal("expected startup-only config change to fail validation")
	}
}

func TestSanitizeGatewayRequestAndInternalMetricsChecks(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("X-User-ID", "spoofed")
	req.Header.Set("X-User-Role", "admin")
	req.Header.Set("X-Request-ID", "spoofed")
	resp := httptest.NewRecorder()

	sanitizeGatewayRequest(resp, req)

	if req.Header.Get("X-User-ID") != "" || req.Header.Get("X-User-Role") != "" {
		t.Fatal("expected sanitizeGatewayRequest to remove spoofed identity headers")
	}
	if requestID := req.Header.Get("X-Request-ID"); requestID == "" || resp.Header().Get("X-Request-ID") != requestID {
		t.Fatal("expected sanitizeGatewayRequest to stamp a fresh request id")
	}

	if !isInternalMetricsClient("127.0.0.1:1234") {
		t.Fatal("expected loopback address to be treated as internal")
	}
	if !isInternalMetricsClient("10.0.0.2:1234") {
		t.Fatal("expected private address to be treated as internal")
	}
	if isInternalMetricsClient("8.8.8.8:53") {
		t.Fatal("expected public address to be rejected as external")
	}
}
