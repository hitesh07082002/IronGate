package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	dto "github.com/prometheus/client_model/go"

	"github.com/hitesh07082002/irongate/internal/config"
	gatewaymetrics "github.com/hitesh07082002/irongate/internal/metrics"
	"github.com/hitesh07082002/irongate/internal/middleware"
	"github.com/hitesh07082002/irongate/internal/ratelimit"
	gwruntime "github.com/hitesh07082002/irongate/internal/runtime"
	"github.com/hitesh07082002/irongate/internal/testutil"
	"github.com/hitesh07082002/irongate/internal/transport"
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

func TestPaymentStatusRouteIsReachableThroughGateway(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")

	cfg, err := config.Load(repoPathFromThisFile("configs", "gateway.yaml"))
	if err != nil {
		t.Fatalf("load gateway config: %v", err)
	}

	paymentsRoute := findRouteByPath(cfg.Routes, "/api/payments")
	if paymentsRoute == nil {
		t.Fatal("expected /api/payments route in gateway config")
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET to reach payment status upstream, got %s", r.Method)
		}
		if r.URL.Path != "/payments/p-1" {
			t.Fatalf("expected upstream path /payments/p-1, got %s", r.URL.Path)
		}
		writeTestJSON(w, http.StatusOK, map[string]string{"id": "p-1", "status": "confirmed"})
	}))
	defer upstream.Close()

	paymentsRoute.Targets = targetsForServers(t, upstream.URL)
	cfg.Routes = []config.RouteConfig{*paymentsRoute}

	gateway := httptest.NewServer(buildHandler(cfg, testLogger()))
	defer gateway.Close()

	req, err := http.NewRequest(http.MethodGet, gateway.URL+"/api/payments/p-1", nil)
	if err != nil {
		t.Fatalf("new payment status request: %v", err)
	}
	req.Header.Set("Authorization", gatewayBearerToken(t, "test-secret", jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  "u-1",
		"role": "admin",
		"iat":  time.Now().Add(-time.Minute).Unix(),
		"exp":  time.Now().Add(time.Hour).Unix(),
	}))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get payment status route: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d with body %s", resp.StatusCode, readBody(t, resp.Body))
	}

	body := readBody(t, resp.Body)
	if !strings.Contains(body, `"id":"p-1"`) || !strings.Contains(body, `"status":"confirmed"`) {
		t.Fatalf("expected payment status payload, got %s", body)
	}
}

func TestRoundRobinLoadBalancingAlternatesTargets(t *testing.T) {
	first := httptest.NewServer(instanceHandler("order-service-1"))
	defer first.Close()

	second := httptest.NewServer(instanceHandler("order-service-2"))
	defer second.Close()

	route := routeForTargets(t, "/api/orders", "/api", "order-service", "round_robin", targetsForServers(t, first.URL, second.URL))
	gateway := httptest.NewServer(buildHandler(testConfig(route), testLogger()))
	defer gateway.Close()

	got := make([]string, 0, 4)
	for range 4 {
		resp, err := http.Get(gateway.URL + "/api/orders")
		if err != nil {
			t.Fatalf("get load-balanced route: %v", err)
		}

		got = append(got, responseInstance(t, resp.Body))
		resp.Body.Close()
	}

	assertSequence(t, got, []string{
		"order-service-1",
		"order-service-2",
		"order-service-1",
		"order-service-2",
	})
}

func TestWeightedLoadBalancingDistributesByWeight(t *testing.T) {
	first := httptest.NewServer(instanceHandler("user-service-1"))
	defer first.Close()

	second := httptest.NewServer(instanceHandler("user-service-2"))
	defer second.Close()

	targets := targetsForServers(t, first.URL, second.URL)
	targets[0].Weight = 3
	targets[1].Weight = 1

	route := routeForTargets(t, "/api/users", "/api", "user-service", "weighted", targets)
	gateway := httptest.NewServer(buildHandler(testConfig(route), testLogger()))
	defer gateway.Close()

	counts := map[string]int{}
	for range 8 {
		resp, err := http.Get(gateway.URL + "/api/users")
		if err != nil {
			t.Fatalf("get weighted route: %v", err)
		}

		counts[responseInstance(t, resp.Body)]++
		resp.Body.Close()
	}

	if counts["user-service-1"] != 6 || counts["user-service-2"] != 2 {
		t.Fatalf("expected 6:2 weighted distribution, got %#v", counts)
	}
}

func TestServedByHeaderMatchesSelectedTarget(t *testing.T) {
	first := httptest.NewServer(instanceHandler("user-service-1"))
	defer first.Close()

	second := httptest.NewServer(instanceHandler("user-service-2"))
	defer second.Close()

	targetHosts := map[string]string{
		"user-service-1": targetHost(t, first.URL),
		"user-service-2": targetHost(t, second.URL),
	}

	route := routeForTargets(t, "/api/users", "/api", "user-service", "round_robin", targetsForServers(t, first.URL, second.URL))
	gateway := httptest.NewServer(buildHandler(testConfig(route), testLogger()))
	defer gateway.Close()

	for range 4 {
		resp, err := http.Get(gateway.URL + "/api/users")
		if err != nil {
			t.Fatalf("get route for served-by check: %v", err)
		}

		instance := responseInstance(t, resp.Body)
		resp.Body.Close()

		if got, want := resp.Header.Get("X-Served-By"), targetHosts[instance]; got != want {
			t.Fatalf("expected X-Served-By %q for instance %q, got %q", want, instance, got)
		}
	}
}

func TestProxyForwardsOriginalHostInXForwardedHost(t *testing.T) {
	var forwardedHost string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwardedHost = r.Header.Get("X-Forwarded-Host")
		writeTestJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	defer upstream.Close()

	gateway := httptest.NewServer(buildHandler(testConfig(routeForServer(t, "/api/users", "/api", "user-service", upstream.URL)), testLogger()))
	defer gateway.Close()

	req, err := http.NewRequest(http.MethodGet, gateway.URL+"/api/users", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = "api.example.com"

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	resp.Body.Close()

	if forwardedHost != "api.example.com" {
		t.Fatalf("expected X-Forwarded-Host %q, got %q", "api.example.com", forwardedHost)
	}
}

func TestRetryConfiguredProtectedRouteExercisesPhaseFivePipeline(t *testing.T) {
	var firstHits int
	var secondHits int
	var forwardedRequestID string
	var forwardedUserID string
	var forwardedUserRole string
	var forwardedAuthorization string

	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		firstHits++
		writeTestJSON(w, http.StatusServiceUnavailable, map[string]string{"instance": "retry-me"})
	}))
	defer first.Close()

	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondHits++
		forwardedRequestID = r.Header.Get(middleware.HeaderRequestID)
		forwardedUserID = r.Header.Get(middleware.HeaderUserID)
		forwardedUserRole = r.Header.Get(middleware.HeaderUserRole)
		forwardedAuthorization = r.Header.Get("Authorization")
		writeTestJSON(w, http.StatusOK, map[string]string{"instance": "healthy"})
	}))
	defer second.Close()

	routePath := uniqueRoutePath("/api/users/retry")
	route := routeForTargets(t, routePath, "/api", "user-service", "round_robin", targetsForServers(t, first.URL, second.URL))
	route.AuthRequired = true
	route.RateLimit = &config.RateLimitConfig{
		Requests: 5,
		Window:   time.Minute,
		Strategy: "sliding_window",
	}
	route.Retry = config.RetryConfig{
		MaxAttempts: 2,
		BaseDelay:   time.Millisecond,
		MaxDelay:    5 * time.Millisecond,
		Jitter:      "full",
	}

	gateway := httptest.NewServer(buildHandlerWithOptions(testConfig(route), testLogger(), buildHandlerOptions{
		rateLimitStore: allowAllRateLimitStore{},
	}))
	defer gateway.Close()

	req, err := http.NewRequest(http.MethodGet, gateway.URL+routePath, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", gatewayBearerToken(t, "test-secret", jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  "u-42",
		"role": "admin",
		"exp":  time.Now().Add(time.Hour).Unix(),
		"iat":  time.Now().Unix(),
	}))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d with body %s", resp.StatusCode, readBody(t, resp.Body))
	}
	if firstHits != 1 || secondHits != 1 {
		t.Fatalf("expected retry across both targets, got first=%d second=%d", firstHits, secondHits)
	}
	if got := responseInstance(t, resp.Body); got != "healthy" {
		t.Fatalf("expected healthy upstream response, got %q", got)
	}
	assertRateLimitHeaders(t, resp, "5", "4")
	if got := resp.Header.Get(transport.HeaderRetryCount); got != "1" {
		t.Fatalf("expected %s=1, got %q", transport.HeaderRetryCount, got)
	}
	if got := resp.Header.Get(transport.HeaderRetryTarget); got != targetHost(t, second.URL) {
		t.Fatalf("expected %s=%q, got %q", transport.HeaderRetryTarget, targetHost(t, second.URL), got)
	}
	if got := resp.Header.Get(transport.HeaderServedBy); got != targetHost(t, second.URL) {
		t.Fatalf("expected %s=%q, got %q", transport.HeaderServedBy, targetHost(t, second.URL), got)
	}
	if _, err := uuid.Parse(forwardedRequestID); err != nil {
		t.Fatalf("expected forwarded request id, got %q", forwardedRequestID)
	}
	if forwardedUserID != "u-42" {
		t.Fatalf("expected forwarded user id u-42, got %q", forwardedUserID)
	}
	if forwardedUserRole != "admin" {
		t.Fatalf("expected forwarded user role admin, got %q", forwardedUserRole)
	}
	if forwardedAuthorization != "" {
		t.Fatalf("expected Authorization header stripped before proxying, got %q", forwardedAuthorization)
	}
}

func TestCircuitOpenFailsOverToHealthyTarget(t *testing.T) {
	var firstHits int
	var secondHits int

	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		firstHits++
		writeTestJSON(w, http.StatusServiceUnavailable, map[string]string{"instance": "opening"})
	}))
	defer first.Close()

	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondHits++
		writeTestJSON(w, http.StatusOK, map[string]string{"instance": "healthy"})
	}))
	defer second.Close()

	routePath := uniqueRoutePath("/api/orders/circuit-open")
	route := routeForTargets(t, routePath, "/api", "order-service", "round_robin", targetsForServers(t, first.URL, second.URL))
	route.Retry = config.RetryConfig{
		MaxAttempts: 2,
		BaseDelay:   time.Millisecond,
		MaxDelay:    5 * time.Millisecond,
		Jitter:      "full",
	}

	cfg := testConfig(route)
	cfg.CircuitBreaker.FailureThreshold = 1
	cfg.CircuitBreaker.SuccessThreshold = 1
	cfg.CircuitBreaker.Timeout = time.Minute
	cfg.CircuitBreaker.HalfOpenMaxRequests = 1

	gateway := httptest.NewServer(buildHandler(cfg, testLogger()))
	defer gateway.Close()

	firstResp := doGatewayRequest(t, gateway, http.MethodGet, routePath, "")
	firstResp.Body.Close()
	if firstResp.StatusCode != http.StatusOK {
		t.Fatalf("expected first request to retry onto healthy target, got %d", firstResp.StatusCode)
	}

	secondResp := doGatewayRequest(t, gateway, http.MethodGet, routePath, "")
	defer secondResp.Body.Close()

	if secondResp.StatusCode != http.StatusOK {
		t.Fatalf("expected second request to fail over around open circuit, got %d with body %s", secondResp.StatusCode, readBody(t, secondResp.Body))
	}
	if firstHits != 1 {
		t.Fatalf("expected open target hit once before tripping circuit, got %d", firstHits)
	}
	if secondHits != 2 {
		t.Fatalf("expected healthy target to serve both successful requests, got %d", secondHits)
	}
	if got := secondResp.Header.Get(transport.HeaderRetryCount); got != "0" {
		t.Fatalf("expected open-circuit failover to keep %s at 0, got %q", transport.HeaderRetryCount, got)
	}
	if got := secondResp.Header.Get(transport.HeaderRetryTarget); got != targetHost(t, second.URL) {
		t.Fatalf("expected %s=%q, got %q", transport.HeaderRetryTarget, targetHost(t, second.URL), got)
	}
}

func TestAllTargetsOpenReturns503NoHealthyTargets(t *testing.T) {
	var firstHits int
	var secondHits int

	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		firstHits++
		writeTestJSON(w, http.StatusServiceUnavailable, map[string]string{"instance": "first-down"})
	}))
	defer first.Close()

	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondHits++
		writeTestJSON(w, http.StatusServiceUnavailable, map[string]string{"instance": "second-down"})
	}))
	defer second.Close()

	routePath := uniqueRoutePath("/api/orders/no-healthy")
	route := routeForTargets(t, routePath, "/api", "order-service", "round_robin", targetsForServers(t, first.URL, second.URL))
	route.Retry = config.RetryConfig{
		MaxAttempts: 2,
		BaseDelay:   time.Millisecond,
		MaxDelay:    5 * time.Millisecond,
		Jitter:      "full",
	}

	cfg := testConfig(route)
	cfg.CircuitBreaker.FailureThreshold = 1
	cfg.CircuitBreaker.SuccessThreshold = 1
	cfg.CircuitBreaker.Timeout = time.Minute
	cfg.CircuitBreaker.HalfOpenMaxRequests = 1

	gateway := httptest.NewServer(buildHandler(cfg, testLogger()))
	defer gateway.Close()

	firstResp := doGatewayRequest(t, gateway, http.MethodGet, routePath, "")
	firstResp.Body.Close()
	if firstResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected first request to surface upstream 503, got %d", firstResp.StatusCode)
	}

	secondResp := doGatewayRequest(t, gateway, http.MethodGet, routePath, "")
	defer secondResp.Body.Close()

	if secondResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when all targets are open, got %d with body %s", secondResp.StatusCode, readBody(t, secondResp.Body))
	}
	if firstHits != 1 || secondHits != 1 {
		t.Fatalf("expected open circuits to prevent extra upstream calls, got first=%d second=%d", firstHits, secondHits)
	}

	var payload struct {
		Error     string `json:"error"`
		Code      int    `json:"code"`
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(secondResp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode no-healthy payload: %v", err)
	}
	if payload.Error != "no healthy targets for service: order-service" {
		t.Fatalf("expected no-healthy error, got %+v", payload)
	}
	if payload.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 error code, got %+v", payload)
	}
	if _, err := uuid.Parse(payload.RequestID); err != nil {
		t.Fatalf("expected request id on 503 payload, got %q", payload.RequestID)
	}
}

func TestPutRetryPreservesRequestBody(t *testing.T) {
	var firstBody string
	var secondBody string

	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read first body: %v", err)
		}
		firstBody = string(body)
		writeTestJSON(w, http.StatusServiceUnavailable, map[string]string{"instance": "retry"})
	}))
	defer first.Close()

	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read second body: %v", err)
		}
		secondBody = string(body)
		writeTestJSON(w, http.StatusOK, map[string]string{"instance": "healthy"})
	}))
	defer second.Close()

	routePath := uniqueRoutePath("/api/users/body-retry")
	route := routeForTargets(t, routePath, "/api", "user-service", "round_robin", targetsForServers(t, first.URL, second.URL))
	route.Methods = []string{http.MethodPut}
	route.Retry = config.RetryConfig{
		MaxAttempts: 2,
		BaseDelay:   time.Millisecond,
		MaxDelay:    5 * time.Millisecond,
		Jitter:      "full",
	}

	gateway := httptest.NewServer(buildHandler(testConfig(route), testLogger()))
	defer gateway.Close()

	payload := `{"id":"u-1","name":"retry"}`
	req, err := http.NewRequest(http.MethodPut, gateway.URL+routePath, strings.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do put request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d with body %s", resp.StatusCode, readBody(t, resp.Body))
	}
	if firstBody != payload || secondBody != payload {
		t.Fatalf("expected identical bodies across retry, got first=%q second=%q", firstBody, secondBody)
	}
	if got := resp.Header.Get(transport.HeaderRetryCount); got != "1" {
		t.Fatalf("expected %s=1, got %q", transport.HeaderRetryCount, got)
	}
}

func TestUnsupportedRateLimitStrategyFailsClosed(t *testing.T) {
	var upstreamHits int

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits++
		writeTestJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	defer upstream.Close()

	route := routeForServer(t, "/api/users", "/api", "user-service", upstream.URL)
	route.RateLimit = &config.RateLimitConfig{
		Requests: 10,
		Window:   time.Minute,
		Strategy: "token_bucket",
	}
	cfg := testConfig(route)
	cfg.Redis.Address = "redis:6379"

	gateway := httptest.NewServer(buildHandler(cfg, testLogger()))
	defer gateway.Close()

	resp, err := http.Get(gateway.URL + "/api/users")
	if err != nil {
		t.Fatalf("get route: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d with body %s", resp.StatusCode, readBody(t, resp.Body))
	}
	var payload struct {
		Error     string `json:"error"`
		Code      int    `json:"code"`
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode unsupported strategy response: %v", err)
	}
	if payload.Error == "" {
		t.Fatalf("expected non-empty error message, got %+v", payload)
	}
	if payload.Code != http.StatusNotImplemented {
		t.Fatalf("expected error code 501, got %+v", payload)
	}
	if _, err := uuid.Parse(payload.RequestID); err != nil {
		t.Fatalf("expected valid request id, got %q", payload.RequestID)
	}
	if upstreamHits != 0 {
		t.Fatalf("expected unsupported strategy to fail before upstream, got %d hits", upstreamHits)
	}
}

func TestProtectedRoutesReturn401WithoutToken(t *testing.T) {
	var upstreamHits int

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits++
		writeTestJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	defer upstream.Close()

	route := routeForServer(t, "/api/users", "/api", "user-service", upstream.URL)
	route.AuthRequired = true

	gateway := httptest.NewServer(buildHandler(testConfig(route), testLogger()))
	defer gateway.Close()

	resp, err := http.Get(gateway.URL + "/api/users")
	if err != nil {
		t.Fatalf("get protected route: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d with body %s", resp.StatusCode, readBody(t, resp.Body))
	}
	if upstreamHits != 0 {
		t.Fatalf("expected gateway auth to block request before upstream, got %d hits", upstreamHits)
	}

	var payload struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode auth error response: %v", err)
	}
	if payload.Error != "missing authorization header" {
		t.Fatalf("expected missing header error, got %q", payload.Error)
	}
}

func TestPublicRoutesRemainPublicInPhaseFive(t *testing.T) {
	testCases := []struct {
		name      string
		build     func(t *testing.T) *httptest.Server
		method    string
		path      string
		wantCode  int
		wantError string
	}{
		{
			name: "login remains public",
			build: func(t *testing.T) *httptest.Server {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					writeTestJSON(w, http.StatusOK, map[string]string{"token": "public-login"})
				}))
				t.Cleanup(upstream.Close)

				route := routeForServer(t, "/api/users/login", "/api", "user-service", upstream.URL)
				route.Methods = []string{http.MethodPost}
				route.AuthRequired = false

				return httptest.NewServer(buildHandler(testConfig(route), testLogger()))
			},
			method:   http.MethodPost,
			path:     "/api/users/login",
			wantCode: http.StatusOK,
		},
		{
			name: "register remains public",
			build: func(t *testing.T) *httptest.Server {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					writeTestJSON(w, http.StatusCreated, map[string]string{"status": "registered"})
				}))
				t.Cleanup(upstream.Close)

				route := routeForServer(t, "/api/users/register", "/api", "user-service", upstream.URL)
				route.Methods = []string{http.MethodPost}
				route.AuthRequired = false

				return httptest.NewServer(buildHandler(testConfig(route), testLogger()))
			},
			method:   http.MethodPost,
			path:     "/api/users/register",
			wantCode: http.StatusCreated,
		},
		{
			name: "health remains public",
			build: func(t *testing.T) *httptest.Server {
				return httptest.NewServer(buildHandler(testConfig(config.RouteConfig{
					Path:         "/health",
					Service:      "gateway-internal",
					AuthRequired: false,
				}), testLogger()))
			},
			method:   http.MethodGet,
			path:     "/health",
			wantCode: http.StatusOK,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			gateway := testCase.build(t)
			defer gateway.Close()

			req, err := http.NewRequest(testCase.method, gateway.URL+testCase.path, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != testCase.wantCode {
				t.Fatalf("expected %d, got %d with body %s", testCase.wantCode, resp.StatusCode, readBody(t, resp.Body))
			}
		})
	}
}

func TestRateLimitingReturnsHeadersAnd429(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	defer upstream.Close()

	routePath := uniqueRoutePath("/api/orders/headers")
	route := routeForServer(t, routePath, "/api", "order-service", upstream.URL)
	route.RateLimit = &config.RateLimitConfig{
		Requests: 2,
		Window:   time.Second,
		Strategy: "sliding_window",
	}
	cfg := testConfig(route)
	cfg.Redis.Address = testutil.RedisAddr(t)

	gateway := httptest.NewServer(buildHandler(cfg, testLogger()))
	defer gateway.Close()

	firstResp := doGatewayRequest(t, gateway, http.MethodGet, routePath, "")
	defer firstResp.Body.Close()
	if firstResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for first request, got %d with body %s", firstResp.StatusCode, readBody(t, firstResp.Body))
	}
	assertRateLimitHeaders(t, firstResp, "2", "1")

	secondResp := doGatewayRequest(t, gateway, http.MethodGet, routePath, "")
	defer secondResp.Body.Close()
	if secondResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for second request, got %d with body %s", secondResp.StatusCode, readBody(t, secondResp.Body))
	}
	assertRateLimitHeaders(t, secondResp, "2", "0")

	thirdResp := doGatewayRequest(t, gateway, http.MethodGet, routePath, "")
	defer thirdResp.Body.Close()
	if thirdResp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for third request, got %d with body %s", thirdResp.StatusCode, readBody(t, thirdResp.Body))
	}
	assertRateLimitHeaders(t, thirdResp, "2", "0")
	if got := thirdResp.Header.Get(middleware.HeaderRetryAfter); got == "" || got == "0" {
		t.Fatalf("expected Retry-After header on 429, got %q", got)
	}
}

func TestRateLimitingUsesAuthenticatedUserIDAndAuthRunsFirst(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	defer upstream.Close()

	routePath := uniqueRoutePath("/api/users/rate-limit-auth")
	route := routeForServer(t, routePath, "/api", "user-service", upstream.URL)
	route.AuthRequired = true
	route.RateLimit = &config.RateLimitConfig{
		Requests: 1,
		Window:   time.Second,
		Strategy: "sliding_window",
	}
	cfg := testConfig(route)
	cfg.Redis.Address = testutil.RedisAddr(t)

	gateway := httptest.NewServer(buildHandler(cfg, testLogger()))
	defer gateway.Close()

	unauthorizedFirst := doGatewayRequest(t, gateway, http.MethodGet, routePath, "")
	defer unauthorizedFirst.Body.Close()
	if unauthorizedFirst.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected first unauthorized request to stay 401, got %d", unauthorizedFirst.StatusCode)
	}

	unauthorizedSecond := doGatewayRequest(t, gateway, http.MethodGet, routePath, "")
	defer unauthorizedSecond.Body.Close()
	if unauthorizedSecond.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected second unauthorized request to stay 401, got %d", unauthorizedSecond.StatusCode)
	}

	userAToken := gatewayBearerToken(t, "test-secret", jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  "u-1",
		"role": "admin",
		"iat":  time.Now().Add(-time.Minute).Unix(),
		"exp":  time.Now().Add(time.Hour).Unix(),
	})
	userBToken := gatewayBearerToken(t, "test-secret", jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  "u-2",
		"role": "admin",
		"iat":  time.Now().Add(-time.Minute).Unix(),
		"exp":  time.Now().Add(time.Hour).Unix(),
	})

	userAFirst := doGatewayRequest(t, gateway, http.MethodGet, routePath, userAToken)
	defer userAFirst.Body.Close()
	if userAFirst.StatusCode != http.StatusOK {
		t.Fatalf("expected first user A request allowed, got %d with body %s", userAFirst.StatusCode, readBody(t, userAFirst.Body))
	}

	userASecond := doGatewayRequest(t, gateway, http.MethodGet, routePath, userAToken)
	defer userASecond.Body.Close()
	if userASecond.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected second user A request rate-limited, got %d with body %s", userASecond.StatusCode, readBody(t, userASecond.Body))
	}

	userBFirst := doGatewayRequest(t, gateway, http.MethodGet, routePath, userBToken)
	defer userBFirst.Body.Close()
	if userBFirst.StatusCode != http.StatusOK {
		t.Fatalf("expected user B to have an independent quota, got %d with body %s", userBFirst.StatusCode, readBody(t, userBFirst.Body))
	}
}

func TestRateLimiterFailsOpenWhenRedisIsDown(t *testing.T) {
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, nil))

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	defer upstream.Close()

	route := routeForServer(t, "/api/orders", "/api", "order-service", upstream.URL)
	route.RateLimit = &config.RateLimitConfig{
		Requests: 1,
		Window:   time.Second,
		Strategy: "sliding_window",
	}
	cfg := testConfig(route)
	cfg.Redis.Address = "127.0.0.1:1"

	gateway := httptest.NewServer(buildHandler(cfg, logger))
	defer gateway.Close()

	resp := doGatewayRequest(t, gateway, http.MethodGet, "/api/orders", "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected fail-open request to succeed, got %d with body %s", resp.StatusCode, readBody(t, resp.Body))
	}
	for _, header := range []string{
		middleware.HeaderRateLimitLimit,
		middleware.HeaderRateLimitRemaining,
		middleware.HeaderRateLimitReset,
		middleware.HeaderRetryAfter,
	} {
		if got := resp.Header.Get(header); got != "" {
			t.Fatalf("expected %s omitted on fail-open, got %q", header, got)
		}
	}
	if !strings.Contains(logBuffer.String(), "rate limit store unavailable; allowing request") {
		t.Fatalf("expected warning log when Redis is down, got %q", logBuffer.String())
	}
}

func TestRateLimitingHonorsTrustedProxyXForwardedFor(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	defer upstream.Close()

	routePath := uniqueRoutePath("/api/orders/trusted-proxy")
	route := routeForServer(t, routePath, "/api", "order-service", upstream.URL)
	route.RateLimit = &config.RateLimitConfig{
		Requests: 1,
		Window:   time.Second,
		Strategy: "sliding_window",
	}
	cfg := testConfig(route)
	cfg.Redis.Address = testutil.RedisAddr(t)

	gateway := httptest.NewServer(buildHandlerWithOptions(cfg, testLogger(), buildHandlerOptions{
		trustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
	}))
	defer gateway.Close()

	first := doGatewayRequestWithForwardedFor(t, gateway, routePath, "198.51.100.10")
	defer first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("expected first forwarded client allowed, got %d", first.StatusCode)
	}

	second := doGatewayRequestWithForwardedFor(t, gateway, routePath, "203.0.113.20")
	defer second.Body.Close()
	if second.StatusCode != http.StatusOK {
		t.Fatalf("expected second forwarded client allowed, got %d with body %s", second.StatusCode, readBody(t, second.Body))
	}

	third := doGatewayRequestWithForwardedFor(t, gateway, routePath, "198.51.100.10")
	defer third.Body.Close()
	if third.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected repeated forwarded client to be rate-limited, got %d with body %s", third.StatusCode, readBody(t, third.Body))
	}
}

func TestProtectedRoutesForwardJWTIdentityAndOverrideSpoofedHeaders(t *testing.T) {
	var forwardedRequestID string
	var forwardedUserID string
	var forwardedUserRole string
	var forwardedAuthorization string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwardedRequestID = r.Header.Get("X-Request-ID")
		forwardedUserID = r.Header.Get("X-User-ID")
		forwardedUserRole = r.Header.Get("X-User-Role")
		forwardedAuthorization = r.Header.Get("Authorization")
		writeTestJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	defer upstream.Close()

	route := routeForServer(t, "/api/users", "/api", "user-service", upstream.URL)
	route.AuthRequired = true

	gateway := httptest.NewServer(buildHandler(testConfig(route), testLogger()))
	defer gateway.Close()

	req, err := http.NewRequest(http.MethodGet, gateway.URL+"/api/users", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", gatewayBearerToken(t, "test-secret", jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  "u-77",
		"role": "support",
		"iat":  time.Now().Add(-time.Minute).Unix(),
		"exp":  time.Now().Add(time.Hour).Unix(),
	}))
	req.Header.Set("X-Request-ID", "spoofed-request-id")
	req.Header.Set("X-User-ID", "attacker")
	req.Header.Set("X-User-Role", "owner")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do protected request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d with body %s", resp.StatusCode, readBody(t, resp.Body))
	}

	responseRequestID := resp.Header.Get("X-Request-ID")
	if _, err := uuid.Parse(responseRequestID); err != nil {
		t.Fatalf("expected valid gateway request id, got %q", responseRequestID)
	}
	if responseRequestID == "spoofed-request-id" {
		t.Fatalf("expected gateway to replace spoofed request id")
	}
	if forwardedRequestID != responseRequestID {
		t.Fatalf("expected forwarded request id %q to match response %q", forwardedRequestID, responseRequestID)
	}
	if forwardedUserID != "u-77" {
		t.Fatalf("expected forwarded X-User-ID %q, got %q", "u-77", forwardedUserID)
	}
	if forwardedUserRole != "support" {
		t.Fatalf("expected forwarded X-User-Role %q, got %q", "support", forwardedUserRole)
	}
	if forwardedAuthorization != "" {
		t.Fatalf("expected Authorization header stripped before proxying, got %q", forwardedAuthorization)
	}
}

func TestLoginToProtectedRouteSuccess(t *testing.T) {
	loginUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]string{
			"token": gatewayBearerTokenValue(t, "test-secret", jwt.SigningMethodHS256, jwt.MapClaims{
				"sub":  "u-1",
				"role": "admin",
				"iat":  time.Now().Add(-time.Minute).Unix(),
				"exp":  time.Now().Add(time.Hour).Unix(),
			}),
		})
	}))
	defer loginUpstream.Close()

	protectedUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-User-ID"); got != "u-1" {
			t.Fatalf("expected protected upstream X-User-ID %q, got %q", "u-1", got)
		}
		if got := r.Header.Get("X-User-Role"); got != "admin" {
			t.Fatalf("expected protected upstream X-User-Role %q, got %q", "admin", got)
		}
		writeTestJSON(w, http.StatusOK, map[string]string{"status": "authorized"})
	}))
	defer protectedUpstream.Close()

	loginRoute := routeForServer(t, "/api/users/login", "/api", "user-login-service", loginUpstream.URL)
	loginRoute.Methods = []string{http.MethodPost}
	loginRoute.AuthRequired = false

	protectedRoute := routeForServer(t, "/api/users", "/api", "user-service", protectedUpstream.URL)
	protectedRoute.AuthRequired = true

	gateway := httptest.NewServer(buildHandler(testConfig(loginRoute, protectedRoute), testLogger()))
	defer gateway.Close()

	loginResp, err := http.Post(gateway.URL+"/api/users/login", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("post login route: %v", err)
	}
	defer loginResp.Body.Close()

	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from login, got %d with body %s", loginResp.StatusCode, readBody(t, loginResp.Body))
	}

	var loginPayload struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&loginPayload); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if loginPayload.Token == "" {
		t.Fatal("expected login response to return a token")
	}

	protectedReq, err := http.NewRequest(http.MethodGet, gateway.URL+"/api/users", nil)
	if err != nil {
		t.Fatalf("new protected request: %v", err)
	}
	protectedReq.Header.Set("Authorization", "Bearer "+loginPayload.Token)

	protectedResp, err := http.DefaultClient.Do(protectedReq)
	if err != nil {
		t.Fatalf("do protected request: %v", err)
	}
	defer protectedResp.Body.Close()

	if protectedResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from protected route, got %d with body %s", protectedResp.StatusCode, readBody(t, protectedResp.Body))
	}
}

func TestMethodRestrictionsAreEnforcedAtGateway(t *testing.T) {
	var upstreamHits int

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		writeTestJSON(w, http.StatusOK, map[string]string{
			"method": r.Method,
		})
	}))
	defer upstream.Close()

	route := routeForServer(t, "/api/users/login", "/api", "user-service", upstream.URL)
	route.Methods = []string{http.MethodPost}

	gateway := httptest.NewServer(buildHandler(testConfig(route), testLogger()))
	defer gateway.Close()

	req, err := http.NewRequest(http.MethodGet, gateway.URL+"/api/users/login", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d with body %s", resp.StatusCode, readBody(t, resp.Body))
	}
	if upstreamHits != 0 {
		t.Fatalf("expected gateway to block request before upstream, got %d upstream hits", upstreamHits)
	}

	allow := resp.Header.Get("Allow")
	if allow != http.MethodPost {
		t.Fatalf("expected Allow header %q, got %q", http.MethodPost, allow)
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

func TestMetricsEndpointRespondsDirectlyAndExportsServiceMetrics(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	defer upstream.Close()

	registry := gatewaymetrics.NewRegistry()
	route := routeForServer(t, "/api/users", "/api", "user-service", upstream.URL)
	route.AuthRequired = true
	route.RateLimit = &config.RateLimitConfig{
		Requests: 10,
		Window:   time.Minute,
		Strategy: "sliding_window",
	}

	cfg := testConfig(route)
	cfg.Metrics.Path = "/internal/metrics"

	gateway := httptest.NewServer(buildHandlerWithOptions(cfg, testLogger(), buildHandlerOptions{
		metricsRegistry: registry,
		rateLimitStore:  allowAllRateLimitStore{},
	}))
	defer gateway.Close()

	resp, err := http.Get(gateway.URL + "/internal/metrics")
	if err != nil {
		t.Fatalf("get metrics before traffic: %v", err)
	}
	body := readBody(t, resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected direct metrics endpoint to return 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(body, "go_goroutines") {
		t.Fatalf("expected Prometheus runtime metrics payload, got %s", body)
	}

	serviceResp := doGatewayRequest(
		t,
		gateway,
		http.MethodGet,
		"/api/users",
		gatewayBearerToken(t, "test-secret", jwt.SigningMethodHS256, jwt.MapClaims{
			"sub":  "u-1",
			"role": "admin",
			"iat":  time.Now().Add(-time.Minute).Unix(),
			"exp":  time.Now().Add(time.Hour).Unix(),
		}),
	)
	defer serviceResp.Body.Close()

	if serviceResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from service route, got %d", serviceResp.StatusCode)
	}

	resp, err = http.Get(gateway.URL + "/internal/metrics")
	if err != nil {
		t.Fatalf("get metrics after traffic: %v", err)
	}
	body = readBody(t, resp.Body)
	resp.Body.Close()

	if !strings.Contains(body, `gateway_requests_total{service="user-service"} 1`) {
		t.Fatalf("expected user-service request metric in payload, got %s", body)
	}
}

func TestMetricsEndpointBypassesAuthAndRateLimiting(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	defer upstream.Close()

	route := routeForServer(t, "/api/users", "/api", "user-service", upstream.URL)
	route.AuthRequired = true
	route.RateLimit = &config.RateLimitConfig{
		Requests: 1,
		Window:   time.Minute,
		Strategy: "sliding_window",
	}

	cfg := testConfig(route)
	cfg.Metrics.Path = "/internal/metrics"

	gateway := httptest.NewServer(buildHandlerWithOptions(cfg, testLogger(), buildHandlerOptions{
		metricsRegistry: gatewaymetrics.NewRegistry(),
		rateLimitStore:  denyAllRateLimitStore{},
	}))
	defer gateway.Close()

	metricsResp, err := http.Get(gateway.URL + "/internal/metrics")
	if err != nil {
		t.Fatalf("get metrics endpoint: %v", err)
	}
	defer metricsResp.Body.Close()

	if metricsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected metrics endpoint to bypass auth and rate limiting with 200, got %d", metricsResp.StatusCode)
	}

	unauthorizedResp := doGatewayRequest(t, gateway, http.MethodGet, "/api/users", "")
	defer unauthorizedResp.Body.Close()

	if unauthorizedResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected protected service route to require auth, got %d", unauthorizedResp.StatusCode)
	}

	authorizedResp := doGatewayRequest(
		t,
		gateway,
		http.MethodGet,
		"/api/users",
		gatewayBearerToken(t, "test-secret", jwt.SigningMethodHS256, jwt.MapClaims{
			"sub":  "u-1",
			"role": "admin",
			"iat":  time.Now().Add(-time.Minute).Unix(),
			"exp":  time.Now().Add(time.Hour).Unix(),
		}),
	)
	defer authorizedResp.Body.Close()

	if authorizedResp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected protected service route to remain rate limited, got %d", authorizedResp.StatusCode)
	}
}

func TestMetricsEndpointRejectsExternalClientsAndSanitizesHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	defer upstream.Close()

	cfg := testConfig(routeForServer(t, "/api/users", "/api", "user-service", upstream.URL))
	cfg.Metrics.Path = "/internal/metrics"

	handler := buildHandlerWithOptions(cfg, testLogger(), buildHandlerOptions{
		metricsRegistry: gatewaymetrics.NewRegistry(),
	})

	req := httptest.NewRequest(http.MethodGet, "/internal/metrics", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	req.Header.Set(middleware.HeaderUserID, "spoofed-user")
	req.Header.Set(middleware.HeaderUserRole, "spoofed-role")
	req.Header.Set(middleware.HeaderRequestID, "spoofed-request")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected external metrics request to be forbidden, got %d", recorder.Code)
	}
	if got := recorder.Header().Get(middleware.HeaderRequestID); got == "" || got == "spoofed-request" {
		t.Fatalf("expected metrics guard to issue a fresh %s, got %q", middleware.HeaderRequestID, got)
	}
	if req.Header.Get(middleware.HeaderUserID) != "" {
		t.Fatalf("expected %s to be stripped", middleware.HeaderUserID)
	}
	if req.Header.Get(middleware.HeaderUserRole) != "" {
		t.Fatalf("expected %s to be stripped", middleware.HeaderUserRole)
	}
	if got := req.Header.Get(middleware.HeaderRequestID); got == "" || got == "spoofed-request" {
		t.Fatalf("expected %s to be replaced with a fresh value, got %q", middleware.HeaderRequestID, got)
	}

	body := readBody(t, recorder.Body)
	if !strings.Contains(body, gwruntime.MetricsInternalOnlyMessage) {
		t.Fatalf("expected metrics rejection body to mention internal-only access, got %s", body)
	}
}

func TestMetricsDisabledSkipsInjectedInstrumentation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	defer upstream.Close()

	registry := gatewaymetrics.NewRegistry()
	cfg := testConfig(routeForServer(t, "/api/users", "/api", "user-service", upstream.URL))
	cfg.Metrics.Enabled = false
	cfg.Metrics.Path = ""

	gateway := httptest.NewServer(buildHandlerWithOptions(cfg, testLogger(), buildHandlerOptions{
		metricsRegistry: registry,
	}))
	defer gateway.Close()

	resp, err := http.Get(gateway.URL + "/api/users")
	if err != nil {
		t.Fatalf("get service route with metrics disabled: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from service route, got %d", resp.StatusCode)
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather registry after disabled metrics request: %v", err)
	}
	if hasMetricForService(families, gatewaymetrics.MetricRequestsTotal, "user-service") {
		t.Fatalf("expected metrics-disabled handler to skip instrumentation even with an injected registry")
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
		Redis: config.RedisConfig{
			Password: "",
			DB:       0,
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

type allowAllRateLimitStore struct{}

func (allowAllRateLimitStore) Allow(_ context.Context, request ratelimit.Request) (ratelimit.Decision, error) {
	now := request.Now
	if now.IsZero() {
		now = time.Now()
	}

	return ratelimit.Decision{
		Allowed:   true,
		Remaining: max(request.Limit-1, 0),
		ResetAt:   now.Add(request.Window),
	}, nil
}

type denyAllRateLimitStore struct{}

func (denyAllRateLimitStore) Allow(_ context.Context, request ratelimit.Request) (ratelimit.Decision, error) {
	now := request.Now
	if now.IsZero() {
		now = time.Now()
	}

	return ratelimit.Decision{
		Allowed:   false,
		Remaining: 0,
		ResetAt:   now.Add(request.Window),
	}, nil
}

func hasMetricForService(families []*dto.MetricFamily, name, service string) bool {
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if label.GetName() == "service" && label.GetValue() == service {
					return true
				}
			}
		}
	}

	return false
}

func doGatewayRequest(t *testing.T, gateway *httptest.Server, method, path, authorization string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(method, gateway.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}

	return resp
}

func doGatewayRequestWithForwardedFor(t *testing.T, gateway *httptest.Server, path, forwardedFor string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, gateway.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Forwarded-For", forwardedFor)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do forwarded-for request: %v", err)
	}

	return resp
}

func assertRateLimitHeaders(t *testing.T, resp *http.Response, wantLimit, wantRemaining string) {
	t.Helper()

	if got := resp.Header.Get(middleware.HeaderRateLimitLimit); got != wantLimit {
		t.Fatalf("expected %s=%q, got %q", middleware.HeaderRateLimitLimit, wantLimit, got)
	}
	if got := resp.Header.Get(middleware.HeaderRateLimitRemaining); got != wantRemaining {
		t.Fatalf("expected %s=%q, got %q", middleware.HeaderRateLimitRemaining, wantRemaining, got)
	}

	reset := resp.Header.Get(middleware.HeaderRateLimitReset)
	if reset == "" {
		t.Fatalf("expected %s header to be set", middleware.HeaderRateLimitReset)
	}
	if _, err := strconv.ParseInt(reset, 10, 64); err != nil {
		t.Fatalf("expected reset header to be a Unix timestamp, got %q: %v", reset, err)
	}
}

func routeForServer(t *testing.T, path, stripPrefix, serviceName, rawURL string) config.RouteConfig {
	t.Helper()

	return routeForTargets(t, path, stripPrefix, serviceName, "round_robin", targetsForServers(t, rawURL))
}

func uniqueRoutePath(base string) string {
	return base + "-" + uuid.NewString()
}

func routeForTargets(t *testing.T, path, stripPrefix, serviceName, loadBalancer string, targets []config.Target) config.RouteConfig {
	t.Helper()

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
		Targets:      append([]config.Target(nil), targets...),
		LoadBalancer: loadBalancer,
	}
}

func targetsForServers(t *testing.T, rawURLs ...string) []config.Target {
	t.Helper()

	targets := make([]config.Target, 0, len(rawURLs))
	for _, rawURL := range rawURLs {
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

		targets = append(targets, config.Target{
			Host: host,
			Port: port,
		})
	}

	return targets
}

func targetHost(t *testing.T, rawURL string) string {
	t.Helper()

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse upstream URL: %v", err)
	}

	return parsedURL.Host
}

func instanceHandler(instance string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusOK, map[string]string{"instance": instance})
	}
}

func responseInstance(t *testing.T, body io.ReadCloser) string {
	t.Helper()

	var payload struct {
		Instance string `json:"instance"`
	}
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		t.Fatalf("decode instance response: %v", err)
	}

	return payload.Instance
}

func gatewayBearerToken(t *testing.T, secret string, method jwt.SigningMethod, claims jwt.MapClaims) string {
	t.Helper()
	return "Bearer " + gatewayBearerTokenValue(t, secret, method, claims)
}

func gatewayBearerTokenValue(t *testing.T, secret string, method jwt.SigningMethod, claims jwt.MapClaims) string {
	t.Helper()

	token := jwt.NewWithClaims(method, claims)
	signedToken, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	return signedToken
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

func assertSequence(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("expected %d items, got %d", len(want), len(got))
	}

	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("unexpected sequence at index %d: got %q want %q (full sequence: %v)", index, got[index], want[index], got)
		}
	}
}

func findRouteByPath(routes []config.RouteConfig, path string) *config.RouteConfig {
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
