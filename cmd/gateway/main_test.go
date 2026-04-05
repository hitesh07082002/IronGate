package main

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hitesh07082002/irongate/internal/config"
)

func TestRoutingReachesConfiguredService(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/1" {
			t.Fatalf("expected upstream path /users/1, got %s", r.URL.Path)
		}
		writeTestJSON(w, http.StatusOK, map[string]string{"id": "u-1"})
	}))
	defer upstream.Close()

	gateway := httptest.NewServer(buildHandler(testConfig(routeForServer(t, "/api/users", "/api", "user-service", upstream.URL)), testLogger()))
	defer gateway.Close()

	resp, err := http.Get(gateway.URL + "/api/users/1")
	if err != nil {
		t.Fatalf("get gateway route: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body := readBody(t, resp.Body)
	if !strings.Contains(body, `"id":"u-1"`) {
		t.Fatalf("expected user payload, got %s", body)
	}
}

func TestOverlappingRoutesPreferLongestPrefix(t *testing.T) {
	var genericHits int
	var loginHits int

	generic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		genericHits++
		writeTestJSON(w, http.StatusOK, map[string]string{"service": "generic"})
	}))
	defer generic.Close()

	login := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		loginHits++
		writeTestJSON(w, http.StatusOK, map[string]string{"service": "login"})
	}))
	defer login.Close()

	routes := []config.RouteConfig{
		routeForServer(t, "/api/users", "/api", "user-service", generic.URL),
		routeForServer(t, "/api/users/login", "/api", "user-login-service", login.URL),
	}
	gateway := httptest.NewServer(buildHandler(testConfig(routes...), testLogger()))
	defer gateway.Close()

	resp, err := http.Post(gateway.URL+"/api/users/login", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("post gateway route: %v", err)
	}
	defer resp.Body.Close()

	if loginHits != 1 {
		t.Fatalf("expected login upstream hit once, got %d", loginHits)
	}
	if genericHits != 0 {
		t.Fatalf("expected generic upstream not to be hit, got %d", genericHits)
	}
}

func TestStripPrefixForwardsExpectedPath(t *testing.T) {
	var forwardedPath string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwardedPath = r.URL.Path
		writeTestJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	defer upstream.Close()

	gateway := httptest.NewServer(buildHandler(testConfig(routeForServer(t, "/api/users", "/api", "user-service", upstream.URL)), testLogger()))
	defer gateway.Close()

	resp, err := http.Get(gateway.URL + "/api/users/1")
	if err != nil {
		t.Fatalf("get gateway route: %v", err)
	}
	defer resp.Body.Close()

	if forwardedPath != "/users/1" {
		t.Fatalf("expected stripped path /users/1, got %q", forwardedPath)
	}
}

func TestUnknownRouteReturnsStandard404JSON(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()

	gateway := httptest.NewServer(buildHandler(testConfig(routeForServer(t, "/api/users", "/api", "user-service", upstream.URL)), testLogger()))
	defer gateway.Close()

	resp, err := http.Get(gateway.URL + "/api/nonexistent")
	if err != nil {
		t.Fatalf("get unknown route: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}

	var payload struct {
		Error     string `json:"error"`
		Code      int    `json:"code"`
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode 404 response: %v", err)
	}

	if payload.Error != "route not found" || payload.Code != http.StatusNotFound {
		t.Fatalf("unexpected error payload: %+v", payload)
	}
	if _, err := uuid.Parse(payload.RequestID); err != nil {
		t.Fatalf("expected valid request id, got %q", payload.RequestID)
	}
}

func TestTracingAddsRequestIDAndSanitizesIncomingHeaders(t *testing.T) {
	var forwardedRequestID string
	var forwardedUserID string
	var forwardedUserRole string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwardedRequestID = r.Header.Get("X-Request-ID")
		forwardedUserID = r.Header.Get("X-User-ID")
		forwardedUserRole = r.Header.Get("X-User-Role")
		writeTestJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	defer upstream.Close()

	gateway := httptest.NewServer(buildHandler(testConfig(routeForServer(t, "/api/users", "/api", "user-service", upstream.URL)), testLogger()))
	defer gateway.Close()

	req, err := http.NewRequest(http.MethodGet, gateway.URL+"/api/users", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Request-ID", "spoofed-request-id")
	req.Header.Set("X-User-ID", "attacker")
	req.Header.Set("X-User-Role", "admin")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	responseRequestID := resp.Header.Get("X-Request-ID")
	if _, err := uuid.Parse(responseRequestID); err != nil {
		t.Fatalf("expected valid response request id, got %q", responseRequestID)
	}
	if responseRequestID == "spoofed-request-id" {
		t.Fatalf("expected gateway to replace spoofed request id")
	}
	if forwardedUserID != "" || forwardedUserRole != "" {
		t.Fatalf("expected user headers stripped, got user_id=%q user_role=%q", forwardedUserID, forwardedUserRole)
	}
	if forwardedRequestID != responseRequestID {
		t.Fatalf("expected forwarded request id %q to match response %q", forwardedRequestID, responseRequestID)
	}
}

func TestStreamingResponsesFlushThroughGateway(t *testing.T) {
	firstChunkFlushed := make(chan struct{})
	allowSecondChunk := make(chan struct{})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		flusher.Flush()
		close(firstChunkFlushed)

		<-allowSecondChunk

		_, _ = io.WriteString(w, "data: second\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	gateway := httptest.NewServer(buildHandler(testConfig(routeForServer(t, "/api/users", "/api", "user-service", upstream.URL)), testLogger()))
	defer gateway.Close()

	resp, err := http.Get(gateway.URL + "/api/users/stream")
	if err != nil {
		t.Fatalf("get streaming route: %v", err)
	}
	defer resp.Body.Close()

	<-firstChunkFlushed

	reader := bufio.NewReader(resp.Body)
	type readResult struct {
		line string
		err  error
	}
	resultCh := make(chan readResult, 1)
	go func() {
		line, err := reader.ReadString('\n')
		resultCh <- readResult{line: line, err: err}
	}()

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("read first streamed chunk: %v", result.err)
		}
		if result.line != "data: first\n" {
			t.Fatalf("expected first streamed chunk, got %q", result.line)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected first streamed chunk before upstream completed")
	}

	close(allowSecondChunk)

	body := readBody(t, reader)
	if !strings.Contains(body, "data: second") {
		t.Fatalf("expected second streamed chunk, got %q", body)
	}
}

func TestRouteWithoutTimeoutUsesServerWriteTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(200 * time.Millisecond):
			writeTestJSON(w, http.StatusOK, map[string]string{"status": "late"})
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()

	cfg := testConfig(routeForServer(t, "/api/users", "/api", "user-service", upstream.URL))
	cfg.Server.WriteTimeout = 50 * time.Millisecond
	cfg.Routes[0].Timeout = 0

	gateway := httptest.NewServer(buildHandler(cfg, testLogger()))
	defer gateway.Close()

	start := time.Now()
	resp, err := http.Get(gateway.URL + "/api/users/1")
	if err != nil {
		t.Fatalf("get timeout route: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("expected 504, got %d with body %s", resp.StatusCode, readBody(t, resp.Body))
	}

	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("expected default timeout to trigger quickly, took %s", elapsed)
	}

	var payload struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode timeout response: %v", err)
	}
	if payload.Error != "upstream request timed out" {
		t.Fatalf("unexpected timeout payload: %+v", payload)
	}
}

func TestGatewayHealthEndpointRespondsDirectly(t *testing.T) {
	gateway := httptest.NewServer(buildHandler(testConfig(config.RouteConfig{
		Path:    "/health",
		Service: "gateway-internal",
	}), testLogger()))
	defer gateway.Close()

	resp, err := http.Get(gateway.URL + "/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body := readBody(t, resp.Body)
	if !strings.Contains(body, `"service":"gateway"`) {
		t.Fatalf("expected gateway health payload, got %s", body)
	}
}

func testConfig(routes ...config.RouteConfig) *config.Config {
	return &config.Config{
		Server: config.ServerConfig{
			Port:         8080,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
		Routes: routes,
		CircuitBreaker: config.CBConfig{
			FailureThreshold:    5,
			SuccessThreshold:    3,
			Timeout:             30 * time.Second,
			WindowSize:          60 * time.Second,
			HalfOpenMaxRequests: 3,
		},
		Auth: config.AuthConfig{
			JWTSecret:    "test-secret",
			JWTAlgorithm: "HS256",
		},
		Metrics: config.MetricsConfig{
			Enabled: true,
			Path:    "/metrics",
		},
		Logging: config.LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

func routeForServer(t *testing.T, path, stripPrefix, serviceName, rawURL string) config.RouteConfig {
	t.Helper()

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}

	host, portValue, err := net.SplitHostPort(parsedURL.Host)
	if err != nil {
		t.Fatalf("split host and port: %v", err)
	}

	port, err := net.LookupPort("tcp", portValue)
	if err != nil {
		t.Fatalf("lookup port: %v", err)
	}

	return config.RouteConfig{
		Path:         path,
		StripPrefix:  stripPrefix,
		Service:      serviceName,
		Methods:      []string{http.MethodGet, http.MethodPost},
		AuthRequired: false,
		Timeout:      30 * time.Second,
		Retry: config.RetryConfig{
			MaxAttempts: 1,
		},
		Targets: []config.Target{
			{
				Host: host,
				Port: port,
			},
		},
		LoadBalancer: "round_robin",
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func writeTestJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func readBody(t *testing.T, body io.Reader) string {
	t.Helper()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}
