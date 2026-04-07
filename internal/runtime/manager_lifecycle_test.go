package runtime

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/hitesh07082002/irongate/internal/response"
)

func TestManagerLifecycleAndGatewayEndpoints(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/1" {
			t.Fatalf("expected forwarded upstream path /users/1, got %s", r.URL.Path)
		}
		response.WriteJSON(w, http.StatusOK, map[string]string{"upstream": "one"})
	}))
	defer upstream.Close()

	manager, err := NewManager(runtimeConfigForServer(t, upstream.URL), BuilderOptions{
		Logger: slog.New(slog.NewTextHandler(os.Stdout, nil)),
	})
	if err != nil {
		t.Fatalf("new runtime manager: %v", err)
	}
	if manager.Current() == nil {
		t.Fatal("expected runtime manager to publish an initial snapshot")
	}
	if !manager.Ready() {
		t.Fatal("expected runtime manager to be ready after startup")
	}

	healthResp := httptest.NewRecorder()
	manager.ServeHTTP(healthResp, httptest.NewRequest(http.MethodGet, HealthPath, nil))
	if healthResp.Code != http.StatusOK {
		t.Fatalf("expected /health 200, got %d", healthResp.Code)
	}

	readyResp := httptest.NewRecorder()
	manager.ServeHTTP(readyResp, httptest.NewRequest(http.MethodGet, ReadyPath, nil))
	if readyResp.Code != http.StatusOK {
		t.Fatalf("expected /ready 200, got %d", readyResp.Code)
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsReq.RemoteAddr = "127.0.0.1:12345"
	metricsResp := httptest.NewRecorder()
	manager.ServeHTTP(metricsResp, metricsReq)
	if metricsResp.Code != http.StatusOK {
		t.Fatalf("expected internal /metrics 200, got %d", metricsResp.Code)
	}

	externalMetricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	externalMetricsReq.RemoteAddr = "8.8.8.8:53"
	externalMetricsResp := httptest.NewRecorder()
	manager.ServeHTTP(externalMetricsResp, externalMetricsReq)
	if externalMetricsResp.Code != http.StatusForbidden {
		t.Fatalf("expected external /metrics 403, got %d", externalMetricsResp.Code)
	}

	routeResp := httptest.NewRecorder()
	routeReq := httptest.NewRequest(http.MethodGet, "/api/users/1", nil)
	manager.ServeHTTP(routeResp, routeReq)
	if routeResp.Code != http.StatusOK {
		t.Fatalf("expected routed request 200, got %d with body %s", routeResp.Code, routeResp.Body.String())
	}

	manager.BeginShutdown()
	if manager.Ready() {
		t.Fatal("expected BeginShutdown to mark manager not ready")
	}

	drainingResp := httptest.NewRecorder()
	manager.ServeHTTP(drainingResp, httptest.NewRequest(http.MethodGet, ReadyPath, nil))
	if drainingResp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected draining /ready 503, got %d", drainingResp.Code)
	}
	var body response.ErrorBody
	if err := json.Unmarshal(drainingResp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode draining response: %v", err)
	}
	if body.Error != gatewayDrainingMessage {
		t.Fatalf("expected draining error %q, got %+v", gatewayDrainingMessage, body)
	}
}

func TestManagerReloadAndReloadFromPathSwapSnapshots(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		response.WriteJSON(w, http.StatusOK, map[string]string{"upstream": "first"})
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		response.WriteJSON(w, http.StatusOK, map[string]string{"upstream": "second"})
	}))
	defer second.Close()

	cfg := runtimeConfigForServer(t, first.URL)
	manager, err := NewManager(cfg, BuilderOptions{})
	if err != nil {
		t.Fatalf("new runtime manager: %v", err)
	}

	secondConfig := runtimeConfigForServer(t, second.URL)
	if err := manager.Reload(secondConfig); err != nil {
		t.Fatalf("reload runtime manager: %v", err)
	}

	secondResp := httptest.NewRecorder()
	manager.ServeHTTP(secondResp, httptest.NewRequest(http.MethodGet, "/api/users/1", nil))
	if secondResp.Code != http.StatusOK || secondResp.Body.String() == "" {
		t.Fatalf("expected reloaded route to respond, got %d with body %s", secondResp.Code, secondResp.Body.String())
	}
	if secondResp.Body.String() == `{"upstream":"first"}`+"\n" {
		t.Fatalf("expected reload to swap upstream, got body %s", secondResp.Body.String())
	}

	configPath := writeRuntimeConfigFile(t, cfg)
	if err := manager.ReloadFromPath(configPath); err != nil {
		t.Fatalf("reload runtime manager from path: %v", err)
	}

	reloadedResp := httptest.NewRecorder()
	manager.ServeHTTP(reloadedResp, httptest.NewRequest(http.MethodGet, "/api/users/1", nil))
	if reloadedResp.Code != http.StatusOK {
		t.Fatalf("expected reload-from-path route 200, got %d", reloadedResp.Code)
	}
	if reloadedResp.Body.String() != `{"upstream":"first"}`+"\n" {
		t.Fatalf("expected reload-from-path to restore first upstream, got %s", reloadedResp.Body.String())
	}
}

func TestNewManagerRejectsNilConfigAndServeHTTPWithoutSnapshot(t *testing.T) {
	if _, err := NewManager(nil, BuilderOptions{}); err == nil {
		t.Fatal("expected nil initial config to fail")
	}

	manager := &Manager{}
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	resp := httptest.NewRecorder()
	manager.ServeHTTP(resp, req)
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected nil snapshot request 503, got %d", resp.Code)
	}
}

func TestManagerConcurrentReloadAndServe(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		response.WriteJSON(w, http.StatusOK, map[string]string{"upstream": "first"})
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		response.WriteJSON(w, http.StatusOK, map[string]string{"upstream": "second"})
	}))
	defer second.Close()

	manager, err := NewManager(runtimeConfigForServer(t, first.URL), BuilderOptions{})
	if err != nil {
		t.Fatalf("new runtime manager: %v", err)
	}

	configs := []*config.Config{
		runtimeConfigForServer(t, first.URL),
		runtimeConfigForServer(t, second.URL),
	}
	start := make(chan struct{})
	errs := make(chan error, 100)
	var wg sync.WaitGroup

	for index := 0; index < 100; index++ {
		cfg := configs[index%len(configs)]
		wg.Add(1)
		go func(cfg *config.Config) {
			defer wg.Done()
			<-start

			for range 5 {
				if manager.Current() == nil {
					errs <- errors.New("expected current snapshot during concurrent access")
					return
				}
				_ = manager.Ready()

				resp := httptest.NewRecorder()
				manager.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/users/1", nil))
				if resp.Code != http.StatusOK {
					errs <- errors.New("expected concurrent ServeHTTP to stay healthy")
					return
				}
				if err := manager.Reload(cfg); err != nil {
					errs <- err
					return
				}
			}
		}(cfg)
	}

	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent manager access failed: %v", err)
	}
}

func runtimeConfigForServer(t *testing.T, rawURL string) *config.Config {
	t.Helper()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}
	host, portValue, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatalf("split upstream host/port: %v", err)
	}
	port, err := strconv.Atoi(portValue)
	if err != nil {
		t.Fatalf("parse upstream port: %v", err)
	}

	return &config.Config{
		Server: config.ServerConfig{
			Port:         8080,
			ReadTimeout:  time.Second,
			WriteTimeout: time.Second,
		},
		Routes: []config.RouteConfig{{
			Path:         "/api/users",
			StripPrefix:  "/api",
			Service:      "user-service",
			Methods:      []string{http.MethodGet},
			LoadBalancer: "round_robin",
			Targets: []config.Target{{
				Host: host,
				Port: port,
			}},
		}},
		Auth: config.AuthConfig{
			JWTSecret:    "test-secret",
			JWTAlgorithm: "HS256",
		},
		Metrics: config.MetricsConfig{
			Enabled: true,
			Path:    "/metrics",
		},
	}
}

func writeRuntimeConfigFile(t *testing.T, cfg *config.Config) string {
	t.Helper()

	rawConfig, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal runtime config: %v", err)
	}

	path := t.TempDir() + "/gateway.yaml"
	if err := os.WriteFile(path, rawConfig, 0o600); err != nil {
		t.Fatalf("write runtime config: %v", err)
	}

	return path
}
