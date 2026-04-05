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
					Requests: -1,
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
	if len(errs) < 4 {
		t.Fatalf("expected multiple validation errors, got %d: %v", len(errs), errs)
	}

	joined := joinErrors(errs)
	assertContains(t, joined, `must define at least one target`)
	assertContains(t, joined, `load_balancer "random" is invalid`)
	assertContains(t, joined, `rate_limit.requests must not be negative`)
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

func TestGatewayConfigPhaseThreeEnablesAuthWithoutLaterPhaseFeatures(t *testing.T) {
	t.Setenv("JWT_SECRET", "")

	cfg, err := Load(repoPathFromThisFile("configs", "gateway.yaml"))
	if err != nil {
		t.Fatalf("load gateway config: %v", err)
	}

	loginRoute := findRouteByPath(cfg.Routes, "/api/users/login")
	registerRoute := findRouteByPath(cfg.Routes, "/api/users/register")
	usersRoute := findRouteByPath(cfg.Routes, "/api/users")
	ordersRoute := findRouteByPath(cfg.Routes, "/api/orders")
	paymentsRoute := findRouteByPath(cfg.Routes, "/api/payments")

	if loginRoute == nil || registerRoute == nil || usersRoute == nil || ordersRoute == nil || paymentsRoute == nil {
		t.Fatalf("expected login, register, users, orders, and payments routes to exist")
	}

	for _, route := range []*RouteConfig{loginRoute, registerRoute} {
		if route.AuthRequired {
			t.Fatalf("expected %s to remain public in Phase 3", route.Path)
		}
		if route.RateLimit != nil {
			t.Fatalf("expected %s to avoid rate-limit config until Phase 4", route.Path)
		}
		if route.Retry.MaxAttempts > 1 {
			t.Fatalf("expected %s to avoid retry config until Phase 5", route.Path)
		}
	}

	for _, route := range []*RouteConfig{usersRoute, ordersRoute, paymentsRoute} {
		if !route.AuthRequired {
			t.Fatalf("expected %s to require auth in Phase 3", route.Path)
		}
		if route.RateLimit != nil {
			t.Fatalf("expected %s to avoid rate-limit config until Phase 4", route.Path)
		}
		if route.Retry.MaxAttempts > 1 {
			t.Fatalf("expected %s to avoid retry config until Phase 5", route.Path)
		}
	}

	if cfg.Auth.JWTSecret != "" {
		t.Fatalf("expected JWT secret to stay environment-backed in checked-in config, got %q", cfg.Auth.JWTSecret)
	}
	if cfg.Auth.JWTAlgorithm != "HS256" {
		t.Fatalf("expected auth.jwt_algorithm %q, got %q", "HS256", cfg.Auth.JWTAlgorithm)
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
