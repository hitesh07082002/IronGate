package config

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestLoadExpandsEnvironmentVariables(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")

	configFile := filepath.Join(t.TempDir(), "gateway.yaml")
	content := `server:
  port: 8080
  read_timeout: 30s
  write_timeout: 30s
routes:
  - path: "/health"
    service: "gateway-internal"
    auth_required: false
circuit_breaker:
  failure_threshold: 5
  success_threshold: 3
  timeout: 30s
  window_size: 60s
  half_open_max_requests: 3
auth:
  jwt_secret: "${JWT_SECRET}"
  jwt_algorithm: "HS256"
redis:
  address: "redis:6379"
  password: ""
  db: 0
metrics:
  enabled: true
  path: "/metrics"
logging:
  level: "info"
  format: "json"
`
	if err := os.WriteFile(configFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configFile)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Auth.JWTSecret != "test-secret" {
		t.Fatalf("expected env expansion, got %q", cfg.Auth.JWTSecret)
	}
}

func TestValidateCollectsMultipleErrors(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Port:         8080,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
		Routes: []RouteConfig{
			{
				Path:         "/api/users",
				Service:      "user-service",
				AuthRequired: true,
				RateLimit: &RateLimitConfig{
					Requests: 0,
					Window:   60 * time.Second,
					Strategy: "sliding_window",
				},
				LoadBalancer: "random",
			},
		},
		CircuitBreaker: CBConfig{
			FailureThreshold:    5,
			SuccessThreshold:    3,
			Timeout:             30 * time.Second,
			WindowSize:          60 * time.Second,
			HalfOpenMaxRequests: 3,
		},
		Auth: AuthConfig{},
	}

	errs := cfg.Validate()
	if len(errs) < 5 {
		t.Fatalf("expected multiple validation errors, got %d: %v", len(errs), errs)
	}

	joined := joinErrors(errs)
	assertContains(t, joined, `must define at least one target`)
	assertContains(t, joined, `load_balancer "random" is invalid`)
	assertContains(t, joined, `rate_limit.requests must be greater than 0`)
	assertContains(t, joined, `redis.address is required when any route has rate_limit configured`)
	assertContains(t, joined, `auth.jwt_secret is required when any route has auth_required: true`)
	assertContains(t, joined, `auth.jwt_algorithm is required when any route has auth_required: true`)
}

func TestValidateAllowsGatewayInternalRouteWithoutTargets(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Port:         8080,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
		Routes: []RouteConfig{
			{
				Path:    "/health",
				Service: gatewayInternalService,
			},
		},
		CircuitBreaker: CBConfig{
			FailureThreshold:    5,
			SuccessThreshold:    3,
			Timeout:             30 * time.Second,
			WindowSize:          60 * time.Second,
			HalfOpenMaxRequests: 3,
		},
	}

	if errs := cfg.Validate(); len(errs) != 0 {
		t.Fatalf("expected no validation errors, got %v", errs)
	}
}

func TestValidateRejectsPortsOutsideTCPRange(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Port:         70000,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
		Routes: []RouteConfig{
			{
				Path:         "/api/users",
				Service:      "user-service",
				LoadBalancer: "round_robin",
				Targets: []Target{
					{
						Host: "user-service-1",
						Port: 70001,
					},
				},
			},
		},
		CircuitBreaker: CBConfig{
			FailureThreshold:    5,
			SuccessThreshold:    3,
			Timeout:             30 * time.Second,
			WindowSize:          60 * time.Second,
			HalfOpenMaxRequests: 3,
		},
	}

	joined := joinErrors(cfg.Validate())
	assertContains(t, joined, "server.port must be between 1 and 65535")
	assertContains(t, joined, `targets[0] port must be between 1 and 65535`)
}

func TestValidateRejectsUnknownRetryJitter(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Port:         8080,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
		Routes: []RouteConfig{
			{
				Path:         "/api/users",
				Service:      "user-service",
				LoadBalancer: "round_robin",
				Retry: RetryConfig{
					MaxAttempts: 2,
					BaseDelay:   100 * time.Millisecond,
					MaxDelay:    time.Second,
					Jitter:      "equal",
				},
				Targets: []Target{
					{
						Host: "user-service-1",
						Port: 8081,
					},
				},
			},
		},
	}

	joined := joinErrors(cfg.Validate())
	assertContains(t, joined, `retry.jitter "equal" is invalid`)
}

func TestValidateRequiresMetricsPathWhenEnabled(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Port:         8080,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
		Routes: []RouteConfig{
			{
				Path:    "/health",
				Service: gatewayInternalService,
			},
		},
		Metrics: MetricsConfig{
			Enabled: true,
		},
	}

	joined := joinErrors(cfg.Validate())
	assertContains(t, joined, "metrics.path is required when metrics.enabled is true")
}

func TestValidateRejectsMetricsPathConflictingWithRoute(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Port:         8080,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
		Routes: []RouteConfig{
			{
				Path:    "/metrics",
				Service: gatewayInternalService,
			},
		},
		Metrics: MetricsConfig{
			Enabled: true,
			Path:    "/metrics",
		},
	}

	joined := joinErrors(cfg.Validate())
	assertContains(t, joined, `metrics.path "/metrics" must not overlap configured route path "/metrics"`)
}

func TestValidateRejectsMetricsPathNestedUnderRoutePrefix(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Port:         8080,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
		Routes: []RouteConfig{
			{
				Path:    "/api",
				Service: gatewayInternalService,
			},
		},
		Metrics: MetricsConfig{
			Enabled: true,
			Path:    "/api/metrics",
		},
	}

	joined := joinErrors(cfg.Validate())
	assertContains(t, joined, `metrics.path "/api/metrics" must not overlap configured route path "/api"`)
}

func TestValidateRejectsMetricsPathThatPrefixesRoute(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Port:         8080,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
		Routes: []RouteConfig{
			{
				Path:    "/metrics/users",
				Service: gatewayInternalService,
			},
		},
		Metrics: MetricsConfig{
			Enabled: true,
			Path:    "/metrics",
		},
	}

	joined := joinErrors(cfg.Validate())
	assertContains(t, joined, `metrics.path "/metrics" must not overlap configured route path "/metrics/users"`)
}

func TestValidateTrimsRoutePathBeforeMetricsConflictChecks(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Port:         8080,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
		Routes: []RouteConfig{
			{
				Path:    "  /metrics  ",
				Service: gatewayInternalService,
			},
		},
		Metrics: MetricsConfig{
			Enabled: true,
			Path:    "/metrics",
		},
	}

	joined := joinErrors(cfg.Validate())
	assertContains(t, joined, `metrics.path "/metrics" must not overlap configured route path "/metrics"`)
}

func TestGatewayConfigPhaseSevenEnablesRuntimeReadinessAndMetrics(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")

	cfg, err := Load(repoPathFromThisFile("configs", "gateway.yaml"))
	if err != nil {
		t.Fatalf("load gateway config: %v", err)
	}

	if errs := cfg.Validate(); len(errs) != 0 {
		t.Fatalf("expected checked-in Phase 7 config to validate, got %v", errs)
	}

	loginRoute := findRouteByPath(cfg.Routes, "/api/users/login")
	registerRoute := findRouteByPath(cfg.Routes, "/api/users/register")
	usersRoute := findRouteByPath(cfg.Routes, "/api/users")
	ordersRoute := findRouteByPath(cfg.Routes, "/api/orders")
	paymentsRoute := findRouteByPath(cfg.Routes, "/api/payments")
	healthRoute := findRouteByPath(cfg.Routes, "/health")
	readyRoute := findRouteByPath(cfg.Routes, "/ready")

	if loginRoute == nil || registerRoute == nil || usersRoute == nil || ordersRoute == nil || paymentsRoute == nil || healthRoute == nil || readyRoute == nil {
		t.Fatalf("expected login, register, users, orders, payments, health, and ready routes to exist")
	}
	if cfg.CircuitBreaker.FailureThreshold != 5 {
		t.Fatalf("expected circuit breaker failure threshold 5, got %d", cfg.CircuitBreaker.FailureThreshold)
	}
	if cfg.CircuitBreaker.SuccessThreshold != 3 {
		t.Fatalf("expected circuit breaker success threshold 3, got %d", cfg.CircuitBreaker.SuccessThreshold)
	}
	if cfg.CircuitBreaker.Timeout != 30*time.Second {
		t.Fatalf("expected circuit breaker timeout 30s, got %s", cfg.CircuitBreaker.Timeout)
	}
	if cfg.CircuitBreaker.WindowSize != 60*time.Second {
		t.Fatalf("expected circuit breaker window size 60s, got %s", cfg.CircuitBreaker.WindowSize)
	}
	if cfg.CircuitBreaker.HalfOpenMaxRequests != 3 {
		t.Fatalf("expected circuit breaker half-open max requests 3, got %d", cfg.CircuitBreaker.HalfOpenMaxRequests)
	}

	for _, route := range []*RouteConfig{loginRoute, registerRoute} {
		if route.AuthRequired {
			t.Fatalf("expected %s to remain public in Phase 5", route.Path)
		}
		if route.RateLimit == nil {
			t.Fatalf("expected %s to keep a Phase 5 rate limit", route.Path)
		}
		if route.RateLimit.Strategy != "sliding_window" {
			t.Fatalf("expected %s rate limit strategy %q, got %q", route.Path, "sliding_window", route.RateLimit.Strategy)
		}
		if route.Retry.MaxAttempts != 0 {
			t.Fatalf("expected %s to use default retry config, got %+v", route.Path, route.Retry)
		}
	}

	for _, route := range []*RouteConfig{usersRoute, ordersRoute, paymentsRoute} {
		if !route.AuthRequired {
			t.Fatalf("expected %s to require auth in Phase 5", route.Path)
		}
		if route.RateLimit == nil {
			t.Fatalf("expected %s to keep a Phase 5 rate limit", route.Path)
		}
		if route.RateLimit.Strategy != "sliding_window" {
			t.Fatalf("expected %s rate limit strategy %q, got %q", route.Path, "sliding_window", route.RateLimit.Strategy)
		}
	}
	if usersRoute.Retry.MaxAttempts != 3 || usersRoute.Retry.BaseDelay != 100*time.Millisecond || usersRoute.Retry.MaxDelay != 2*time.Second || usersRoute.Retry.Jitter != "full" {
		t.Fatalf("expected /api/users retry config to be live in Phase 5, got %+v", usersRoute.Retry)
	}
	if ordersRoute.Retry.MaxAttempts != 3 || ordersRoute.Retry.BaseDelay != 100*time.Millisecond || ordersRoute.Retry.MaxDelay != 2*time.Second || ordersRoute.Retry.Jitter != "full" {
		t.Fatalf("expected /api/orders retry config to be live in Phase 5, got %+v", ordersRoute.Retry)
	}
	if paymentsRoute.Retry.MaxAttempts != 1 {
		t.Fatalf("expected /api/payments retries disabled by default, got %+v", paymentsRoute.Retry)
	}

	if cfg.Auth.JWTSecret != "test-secret" {
		t.Fatalf("expected JWT secret to expand from environment, got %q", cfg.Auth.JWTSecret)
	}
	if cfg.Auth.JWTAlgorithm != "HS256" {
		t.Fatalf("expected auth.jwt_algorithm %q, got %q", "HS256", cfg.Auth.JWTAlgorithm)
	}
	if cfg.Redis.Address != "redis:6379" {
		t.Fatalf("expected redis.address %q, got %q", "redis:6379", cfg.Redis.Address)
	}
	if !cfg.Metrics.Enabled {
		t.Fatal("expected metrics.enabled to be true in the checked-in config")
	}
	if cfg.Metrics.Path != "/metrics" {
		t.Fatalf("expected metrics.path %q, got %q", "/metrics", cfg.Metrics.Path)
	}
	if healthRoute.RateLimit != nil {
		t.Fatalf("expected /health to remain exempt from rate limiting")
	}
	if readyRoute.RateLimit != nil {
		t.Fatalf("expected /ready to remain exempt from rate limiting")
	}

	assertRouteMethods(t, paymentsRoute, "GET", "POST")
}

func joinErrors(errs []error) string {
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		parts = append(parts, err.Error())
	}
	return strings.Join(parts, "\n")
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected %q to contain %q", got, want)
	}
}

func assertRouteMethods(t *testing.T, route *RouteConfig, methods ...string) {
	t.Helper()

	got := normalizeMethods(route.Methods)
	want := normalizeMethods(methods)
	if len(got) != len(want) {
		t.Fatalf("expected %s to expose methods %v, got %v", route.Path, want, got)
	}

	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("expected %s to expose methods %v, got %v", route.Path, want, got)
		}
	}
}

func normalizeMethods(methods []string) []string {
	normalized := make([]string, len(methods))
	for index, method := range methods {
		normalized[index] = strings.ToUpper(strings.TrimSpace(method))
	}
	sort.Strings(normalized)
	return normalized
}

func findRouteByPath(routes []RouteConfig, path string) *RouteConfig {
	for index := range routes {
		if routes[index].Path == path {
			return &routes[index]
		}
	}

	return nil
}

func repoPathFromThisFile(parts ...string) string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed")
	}

	base := filepath.Dir(filename)
	pathParts := append([]string{base, "..", ".."}, parts...)
	return filepath.Join(pathParts...)
}
