package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/hitesh07082002/irongate/internal/config"
	gwruntime "github.com/hitesh07082002/irongate/internal/runtime"
)

func TestConfigWatcherReloadsRuntimeSnapshot(t *testing.T) {
	originalUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]string{"instance": "original"})
	}))
	defer originalUpstream.Close()

	reloadedUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]string{"instance": "reloaded"})
	}))
	defer reloadedUpstream.Close()

	configPath := filepath.Join(t.TempDir(), "gateway.yaml")
	writePhaseSevenConfig(t, configPath, phaseSevenConfigOptions{
		UpstreamURL:  originalUpstream.URL,
		AuthRequired: false,
		JWTSecret:    "initial-secret",
	})

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load initial config: %v", err)
	}

	manager, err := newRuntimeManager(cfg, testLogger(), buildHandlerOptions{})
	if err != nil {
		t.Fatalf("build runtime manager: %v", err)
	}

	gateway := httptest.NewServer(manager)
	defer gateway.Close()

	cancelWatcher := startRuntimeWatcher(t, configPath, manager, testLogger())
	defer cancelWatcher()

	initialResp := doGatewayRequest(t, gateway, http.MethodGet, "/api/users/1", "")
	defer initialResp.Body.Close()
	if got := responseInstance(t, initialResp.Body); got != "original" {
		t.Fatalf("expected original upstream before reload, got %q", got)
	}

	writePhaseSevenConfig(t, configPath, phaseSevenConfigOptions{
		UpstreamURL:  reloadedUpstream.URL,
		AuthRequired: true,
		JWTSecret:    "reloaded-secret",
	})

	waitForCondition(t, 3*time.Second, func() (bool, string) {
		resp := doGatewayRequest(t, gateway, http.MethodGet, "/api/users/1", "")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			return false, fmt.Sprintf("expected 401 after reload, got %d with body %s", resp.StatusCode, readBody(t, resp.Body))
		}

		return true, ""
	})

	authorizedResp := doGatewayRequest(
		t,
		gateway,
		http.MethodGet,
		"/api/users/1",
		gatewayBearerToken(t, "reloaded-secret", jwt.SigningMethodHS256, jwt.MapClaims{
			"sub":  "phase7-user",
			"role": "admin",
			"iat":  time.Now().Add(-time.Minute).Unix(),
			"exp":  time.Now().Add(time.Hour).Unix(),
		}),
	)
	defer authorizedResp.Body.Close()

	if authorizedResp.StatusCode != http.StatusOK {
		t.Fatalf("expected authorized request after reload to succeed, got %d with body %s", authorizedResp.StatusCode, readBody(t, authorizedResp.Body))
	}
	if got := responseInstance(t, authorizedResp.Body); got != "reloaded" {
		t.Fatalf("expected reloaded upstream after reload, got %q", got)
	}
}

func TestInvalidReloadKeepsPreviousSnapshotReady(t *testing.T) {
	var logBuffer safeBuffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, nil))

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]string{"instance": "stable"})
	}))
	defer upstream.Close()

	configPath := filepath.Join(t.TempDir(), "gateway.yaml")
	writePhaseSevenConfig(t, configPath, phaseSevenConfigOptions{
		UpstreamURL:  upstream.URL,
		AuthRequired: false,
		JWTSecret:    "stable-secret",
	})

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load initial config: %v", err)
	}

	manager, err := newRuntimeManager(cfg, logger, buildHandlerOptions{})
	if err != nil {
		t.Fatalf("build runtime manager: %v", err)
	}

	gateway := httptest.NewServer(manager)
	defer gateway.Close()

	cancelWatcher := startRuntimeWatcher(t, configPath, manager, logger)
	defer cancelWatcher()

	if err := os.WriteFile(configPath, []byte("server:\n  port: [\n"), 0o600); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}

	waitForCondition(t, 3*time.Second, func() (bool, string) {
		if strings.Contains(logBuffer.String(), "config reload failed; keeping previous runtime snapshot") {
			return true, ""
		}

		return false, "waiting for watcher failure log"
	})

	resp := doGatewayRequest(t, gateway, http.MethodGet, "/api/users/1", "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected previous snapshot to keep serving traffic, got %d with body %s", resp.StatusCode, readBody(t, resp.Body))
	}
	if got := responseInstance(t, resp.Body); got != "stable" {
		t.Fatalf("expected stable upstream after invalid reload, got %q", got)
	}

	readyResp, err := http.Get(gateway.URL + "/ready")
	if err != nil {
		t.Fatalf("get readiness after invalid reload: %v", err)
	}
	defer readyResp.Body.Close()

	if readyResp.StatusCode != http.StatusOK {
		t.Fatalf("expected readiness to stay 200 after invalid reload, got %d with body %s", readyResp.StatusCode, readBody(t, readyResp.Body))
	}
}

func TestReadinessTransitionsDuringGracefulShutdown(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseResponse := make(chan struct{})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-requestStarted:
		default:
			close(requestStarted)
		}

		<-releaseResponse
		writeTestJSON(w, http.StatusOK, map[string]string{"status": "completed"})
	}))
	defer upstream.Close()

	cfg := testConfig(routeForServer(t, "/api/users", "/api", "user-service", upstream.URL))
	cfg.Server.WriteTimeout = 2 * time.Second

	manager, err := newRuntimeManager(cfg, testLogger(), buildHandlerOptions{})
	if err != nil {
		t.Fatalf("build runtime manager: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	server := &http.Server{
		Handler:      manager,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.Serve(listener)
	}()

	baseURL := "http://" + listener.Addr().String()

	readyBefore, err := http.Get(baseURL + "/ready")
	if err != nil {
		t.Fatalf("get initial readiness: %v", err)
	}
	if readyBefore.StatusCode != http.StatusOK {
		t.Fatalf("expected readiness 200 before drain, got %d with body %s", readyBefore.StatusCode, readBody(t, readyBefore.Body))
	}
	readyBefore.Body.Close()

	longResponse := make(chan *http.Response, 1)
	longErrors := make(chan error, 1)
	go func() {
		resp, err := http.Get(baseURL + "/api/users/slow")
		if err != nil {
			longErrors <- err
			return
		}
		longResponse <- resp
	}()

	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for in-flight request to start")
	}

	manager.BeginShutdown()

	readyDuringDrain, err := http.Get(baseURL + "/ready")
	if err != nil {
		t.Fatalf("get readiness during drain: %v", err)
	}
	if readyDuringDrain.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected readiness 503 during drain, got %d with body %s", readyDuringDrain.StatusCode, readBody(t, readyDuringDrain.Body))
	}
	readyDuringDrain.Body.Close()

	healthDuringDrain, err := http.Get(baseURL + "/health")
	if err != nil {
		t.Fatalf("get health during drain: %v", err)
	}
	if healthDuringDrain.StatusCode != http.StatusOK {
		t.Fatalf("expected liveness 200 during drain, got %d with body %s", healthDuringDrain.StatusCode, readBody(t, healthDuringDrain.Body))
	}
	healthDuringDrain.Body.Close()

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout(cfg.Server.WriteTimeout))
		defer cancel()
		shutdownDone <- server.Shutdown(shutdownCtx)
	}()

	select {
	case err := <-shutdownDone:
		t.Fatalf("expected shutdown to wait for in-flight request, returned early with %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	close(releaseResponse)

	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("graceful shutdown failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for graceful shutdown to complete")
	}

	select {
	case err := <-longErrors:
		t.Fatalf("in-flight request failed during shutdown: %v", err)
	case resp := <-longResponse:
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected in-flight request to complete during shutdown, got %d with body %s", resp.StatusCode, readBody(t, resp.Body))
		}
		if !strings.Contains(readBody(t, resp.Body), `"status":"completed"`) {
			t.Fatalf("expected completed payload from in-flight request")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for in-flight response after shutdown")
	}

	if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("unexpected server error after shutdown: %v", err)
	}
}

func TestReloadPreservesCircuitBreakerState(t *testing.T) {
	var upstreamHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits.Add(1)
		writeTestJSON(w, http.StatusInternalServerError, map[string]string{"error": "boom"})
	}))
	defer upstream.Close()

	route := routeForServer(t, "/api/users", "/api", "user-service", upstream.URL)
	route.Methods = []string{http.MethodGet}

	cfg := testConfig(route)
	cfg.Metrics.Enabled = false
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

	gateway := httptest.NewServer(manager)
	defer gateway.Close()

	firstResp := doGatewayRequest(t, gateway, http.MethodGet, "/api/users/1", "")
	defer firstResp.Body.Close()
	if firstResp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected first request to return upstream 500, got %d with body %s", firstResp.StatusCode, readBody(t, firstResp.Body))
	}

	reloaded := cfg.Clone()
	reloaded.Metrics.Enabled = true
	reloaded.Metrics.Path = "/internal/metrics"
	if err := manager.Reload(reloaded); err != nil {
		t.Fatalf("reload runtime manager: %v", err)
	}

	secondResp := doGatewayRequest(t, gateway, http.MethodGet, "/api/users/1", "")
	defer secondResp.Body.Close()
	if secondResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected reloaded runtime to preserve open circuit and return 503, got %d with body %s", secondResp.StatusCode, readBody(t, secondResp.Body))
	}
	if upstreamHits.Load() != 1 {
		t.Fatalf("expected open circuit to block another upstream attempt after reload, got %d upstream hits", upstreamHits.Load())
	}
}

type phaseSevenConfigOptions struct {
	UpstreamURL  string
	AuthRequired bool
	JWTSecret    string
	WriteTimeout time.Duration
	RouteTimeout time.Duration
	ReadTimeout  time.Duration
}

type safeBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func writePhaseSevenConfig(t *testing.T, path string, options phaseSevenConfigOptions) {
	t.Helper()

	readTimeout := options.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = 2 * time.Second
	}
	writeTimeout := options.WriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = 2 * time.Second
	}
	routeTimeout := options.RouteTimeout
	if routeTimeout <= 0 {
		routeTimeout = 2 * time.Second
	}

	target := targetsForServers(t, options.UpstreamURL)[0]
	configData := fmt.Sprintf(`server:
  port: 8080
  read_timeout: %s
  write_timeout: %s

routes:
  - path: "/api/users"
    strip_prefix: "/api"
    service: "user-service"
    methods: ["GET"]
    auth_required: %t
    timeout: %s
    retry:
      max_attempts: 1
    targets:
      - host: "%s"
        port: %d
    load_balancer: "round_robin"

  - path: "/health"
    service: "gateway-internal"
    auth_required: false

  - path: "/ready"
    service: "gateway-internal"
    auth_required: false

auth:
  jwt_secret: "%s"
  jwt_algorithm: "HS256"

metrics:
  enabled: false
`, readTimeout, writeTimeout, options.AuthRequired, routeTimeout, target.Host, target.Port, options.JWTSecret)

	if err := os.WriteFile(path, []byte(configData), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
}

func startRuntimeWatcher(t *testing.T, configPath string, manager *gwruntime.Manager, logger *slog.Logger) context.CancelFunc {
	t.Helper()

	watcher, err := gwruntime.NewWatcher(configPath, manager, logger, 25*time.Millisecond)
	if err != nil {
		t.Fatalf("create config watcher: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		if err := watcher.Run(ctx); err != nil {
			panic(err)
		}
	}()

	time.Sleep(50 * time.Millisecond)
	return cancel
}

func waitForCondition(t *testing.T, timeout time.Duration, check func() (bool, string)) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	lastMessage := "condition did not succeed"
	for time.Now().Before(deadline) {
		ok, message := check()
		if ok {
			return
		}
		if message != "" {
			lastMessage = message
		}
		time.Sleep(25 * time.Millisecond)
	}

	t.Fatalf("timed out after %s: %s", timeout, lastMessage)
}
