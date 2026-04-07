package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestHTTPClient(fn roundTripFunc) *http.Client {
	return &http.Client{Transport: fn}
}

func newTestScenario() *Scenario {
	return &Scenario{
		Name:             "happy-path",
		DisplayName:      "Happy Path",
		Category:         "baseline",
		DurationOptions:  []int{30, 60},
		IntensityOptions: map[string]IntensityOption{"mild": {RPS: 10}},
		K6Script:         "scenarios/k6/happy-path.js",
	}
}

func newTestApp(t *testing.T) *app {
	t.Helper()

	scenario := newTestScenario()
	return &app{
		logger: newTestLogger(),
		httpClient: newTestHTTPClient(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}),
		demoToken:      "demo-token",
		adminToken:     "admin-token",
		demoJWT:        "demo-jwt",
		scenarios:      map[string]*Scenario{scenario.Name: scenario},
		scenarioStatus: map[string]scenarioStatus{scenario.Name: statusIdle},
		eventHub:       NewEventHub(newTestLogger()),
		globalLimiter:  NewIPRateLimiter(1000, time.Minute),
		metricsLimiter: NewIPRateLimiter(1000, time.Minute),
		runner:         &Runner{},
		toxiproxy: NewToxiproxyClient(newTestHTTPClient(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}), newTestLogger()),
	}
}

func decodeJSONResponse[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()

	var payload T
	if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return payload
}

func assertAPIErrorResponse(t *testing.T, recorder *httptest.ResponseRecorder, status int) apiError {
	t.Helper()

	payload := decodeJSONResponse[apiError](t, recorder)
	if payload.Code != status {
		t.Fatalf("error payload code = %d, want %d", payload.Code, status)
	}
	if strings.TrimSpace(payload.Error) == "" {
		t.Fatalf("expected error payload message, got %#v", payload)
	}
	if strings.TrimSpace(payload.RequestID) == "" {
		t.Fatalf("expected request_id in error payload, got %#v", payload)
	}

	return payload
}

func TestRoutesServeHealthAndScenarioEndpoints(t *testing.T) {
	app := newTestApp(t)
	handler := app.routes()

	request := func(method, target string, body io.Reader) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, target, body)
		req.RemoteAddr = "127.0.0.1:1234"
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}

	health := request(http.MethodGet, "/api/health", nil)
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", health.Code, http.StatusOK)
	}
	healthPayload := decodeJSONResponse[healthResponse](t, health)
	if !healthPayload.JWTValid || healthPayload.SpecVersion != observatorySpecVersion {
		t.Fatalf("unexpected health payload: %#v", healthPayload)
	}

	list := request(http.MethodGet, "/api/scenarios", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d", list.Code, http.StatusOK)
	}
	listPayload := decodeJSONResponse[[]scenarioListItem](t, list)
	if len(listPayload) != 1 || listPayload[0].Name != "happy-path" {
		t.Fatalf("unexpected scenario list: %#v", listPayload)
	}

	getScenario := request(http.MethodGet, "/api/scenarios/happy-path", nil)
	if getScenario.Code != http.StatusOK {
		t.Fatalf("get scenario status = %d, want %d", getScenario.Code, http.StatusOK)
	}
	scenarioPayload := decodeJSONResponse[Scenario](t, getScenario)
	if scenarioPayload.Name != "happy-path" {
		t.Fatalf("unexpected scenario payload: %#v", scenarioPayload)
	}

	status := request(http.MethodGet, "/api/scenarios/happy-path/status", nil)
	if status.Code != http.StatusOK {
		t.Fatalf("scenario status code = %d, want %d", status.Code, http.StatusOK)
	}
	statusPayload := decodeJSONResponse[map[string]scenarioStatus](t, status)
	if statusPayload["status"] != statusIdle {
		t.Fatalf("expected idle scenario status, got %#v", statusPayload)
	}

	notFound := request(http.MethodGet, "/api/scenarios/missing", nil)
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("missing scenario status = %d, want %d", notFound.Code, http.StatusNotFound)
	}

	missingStatus := request(http.MethodGet, "/api/scenarios/missing/status", nil)
	if missingStatus.Code != http.StatusNotFound {
		t.Fatalf("missing scenario status endpoint = %d, want %d", missingStatus.Code, http.StatusNotFound)
	}
}

func TestRunScenarioHandlerValidationAndFailures(t *testing.T) {
	app := newTestApp(t)
	handler := app.routes()

	request := func(body string, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/scenarios/happy-path/run", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1234"
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}

	unauthorized := request(`{"intensity":"mild","duration":30}`, "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized run status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}
	assertAPIErrorResponse(t, unauthorized, http.StatusUnauthorized)

	invalidBody := request(`{`, app.demoToken)
	if invalidBody.Code != http.StatusBadRequest {
		t.Fatalf("invalid body status = %d, want %d", invalidBody.Code, http.StatusBadRequest)
	}
	assertAPIErrorResponse(t, invalidBody, http.StatusBadRequest)

	invalidParams := request(`{"intensity":"missing","duration":30}`, app.demoToken)
	if invalidParams.Code != http.StatusBadRequest {
		t.Fatalf("invalid params status = %d, want %d", invalidParams.Code, http.StatusBadRequest)
	}
	assertAPIErrorResponse(t, invalidParams, http.StatusBadRequest)

	app.active = &scenarioRun{name: "happy-path"}
	conflict := request(`{"intensity":"mild","duration":30}`, app.demoToken)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, want %d", conflict.Code, http.StatusConflict)
	}
	assertAPIErrorResponse(t, conflict, http.StatusConflict)

	app.active = nil
	recorder := request(`{"intensity":"mild","duration":30}`, app.demoToken)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("runner failure status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	assertAPIErrorResponse(t, recorder, http.StatusInternalServerError)
	if got := app.scenarioStatusFor("happy-path"); got != statusError {
		t.Fatalf("scenario status after runner failure = %q, want %q", got, statusError)
	}
}

func TestStartStopAndCompleteScenarioLifecycle(t *testing.T) {
	app := newTestApp(t)
	scenario := app.scenarios["happy-path"]

	app.active = &scenarioRun{name: scenario.Name}
	if err := app.startScenario(scenario, runParams{Intensity: "mild", Duration: 30}); !errors.Is(err, errScenarioAlreadyRunning) {
		t.Fatalf("startScenario error = %v, want %v", err, errScenarioAlreadyRunning)
	}

	app.active = &scenarioRun{name: scenario.Name, containerID: "", cancel: func() {}}
	if err := app.stopScenario(context.Background(), "other"); !errors.Is(err, errScenarioNotRunning) {
		t.Fatalf("stopScenario mismatch error = %v, want %v", err, errScenarioNotRunning)
	}

	var canceled atomic.Bool
	app.active = &scenarioRun{
		name:        scenario.Name,
		containerID: "",
		cancel:      func() { canceled.Store(true) },
	}
	app.scenarioStatus[scenario.Name] = statusRunning
	if err := app.stopScenario(context.Background(), scenario.Name); err != nil {
		t.Fatalf("stopScenario: %v", err)
	}
	if !canceled.Load() {
		t.Fatal("expected stopScenario to cancel the active run")
	}
	if got := app.scenarioStatusFor(scenario.Name); got != statusStopping {
		t.Fatalf("scenario status after stop = %q, want %q", got, statusStopping)
	}

	successRun := &scenarioRun{name: scenario.Name}
	app.active = successRun
	app.scenarioStatus[scenario.Name] = statusRunning
	app.completeScenario(successRun, scenario.Name, nil)
	if app.active != nil {
		t.Fatal("expected completeScenario success to clear active run")
	}
	if got := app.scenarioStatusFor(scenario.Name); got != statusIdle {
		t.Fatalf("scenario status after success = %q, want %q", got, statusIdle)
	}

	errorRun := &scenarioRun{name: scenario.Name}
	app.active = errorRun
	app.scenarioStatus[scenario.Name] = statusRunning
	app.completeScenario(errorRun, scenario.Name, errors.New("boom"))
	if got := app.scenarioStatusFor(scenario.Name); got != statusError {
		t.Fatalf("scenario status after failure = %q, want %q", got, statusError)
	}

	snapshot, _, cancel := app.eventHub.Subscribe()
	defer cancel()
	if len(snapshot) < 2 {
		t.Fatalf("expected scenario lifecycle events, got %d", len(snapshot))
	}
}

func TestStopScenarioHandler(t *testing.T) {
	app := newTestApp(t)
	handler := app.routes()

	req := httptest.NewRequest(http.MethodPost, "/api/scenarios/happy-path/stop", nil)
	req.Header.Set("Authorization", "Bearer "+app.demoToken)
	req.RemoteAddr = "127.0.0.1:4444"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("stop handler status = %d, want %d", recorder.Code, http.StatusOK)
	}
	payload := decodeJSONResponse[map[string]scenarioStatus](t, recorder)
	if payload["status"] != statusIdle {
		t.Fatalf("unexpected stop payload: %#v", payload)
	}

	app.active = &scenarioRun{
		name:        "happy-path",
		containerID: "",
		cancel:      func() {},
	}
	app.scenarioStatus["happy-path"] = statusRunning

	stopActiveReq := httptest.NewRequest(http.MethodPost, "/api/scenarios/happy-path/stop", nil)
	stopActiveReq.Header.Set("Authorization", "Bearer "+app.demoToken)
	stopActiveReq.RemoteAddr = "127.0.0.1:4444"
	stopActiveRecorder := httptest.NewRecorder()
	handler.ServeHTTP(stopActiveRecorder, stopActiveReq)
	if stopActiveRecorder.Code != http.StatusOK {
		t.Fatalf("active stop handler status = %d, want %d", stopActiveRecorder.Code, http.StatusOK)
	}
	activePayload := decodeJSONResponse[map[string]scenarioStatus](t, stopActiveRecorder)
	if activePayload["status"] != statusStopping {
		t.Fatalf("unexpected active stop payload: %#v", activePayload)
	}

	missingReq := httptest.NewRequest(http.MethodPost, "/api/scenarios/missing/stop", nil)
	missingReq.Header.Set("Authorization", "Bearer "+app.demoToken)
	missingReq.RemoteAddr = "127.0.0.1:4444"
	missingRecorder := httptest.NewRecorder()
	handler.ServeHTTP(missingRecorder, missingReq)
	if missingRecorder.Code != http.StatusNotFound {
		t.Fatalf("missing stop handler status = %d, want %d", missingRecorder.Code, http.StatusNotFound)
	}
}

func TestUtilityHelpers(t *testing.T) {
	if authorizedBearerToken(nil, "demo-token") {
		t.Fatal("expected nil request to fail auth")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/reset", nil)
	req.Header.Set("Authorization", "Bearer demo-token")
	if !authorizedBearerToken(req, "demo-token") {
		t.Fatal("expected bearer token to authorize")
	}
	req.Header.Set("Authorization", "Basic demo-token")
	if authorizedBearerToken(req, "demo-token") {
		t.Fatal("expected non-bearer auth to fail")
	}

	recorder := httptest.NewRecorder()
	writeJSON(recorder, http.StatusAccepted, map[string]string{"status": "ok"})
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q, want application/json", got)
	}

	errorRecorder := httptest.NewRecorder()
	writeAPIError(errorRecorder, http.StatusForbidden, "denied")
	apiErr := decodeJSONResponse[apiError](t, errorRecorder)
	if apiErr.Code != http.StatusForbidden || apiErr.Error != "denied" {
		t.Fatalf("unexpected api error payload: %#v", apiErr)
	}

	t.Setenv("TEST_REQUIRED_ENV", " configured ")
	value, err := requiredEnv("TEST_REQUIRED_ENV")
	if err != nil || value != "configured" {
		t.Fatalf("requiredEnv returned (%q, %v)", value, err)
	}
	t.Setenv("TEST_REQUIRED_ENV", "")
	if _, err := requiredEnv("TEST_REQUIRED_ENV"); err == nil {
		t.Fatal("expected requiredEnv to fail when unset")
	}

	t.Setenv(projectRootEnvVar, filepath.Clean("/tmp/observatory"))
	if got, err := resolveHostProjectRoot(); err != nil || got != "/tmp/observatory" {
		t.Fatalf("resolveHostProjectRoot env returned (%q, %v)", got, err)
	}
	t.Setenv(projectRootEnvVar, "")
	if got, err := resolveHostProjectRoot(); err != nil || strings.TrimSpace(got) == "" {
		t.Fatalf("resolveHostProjectRoot cwd returned (%q, %v)", got, err)
	}

	if got := clientIPFromRequest(nil); got != "unknown" {
		t.Fatalf("clientIPFromRequest(nil) = %q, want unknown", got)
	}
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:4321"
	if got := clientIPFromRequest(req); got != "127.0.0.1" {
		t.Fatalf("clientIPFromRequest host:port = %q, want 127.0.0.1", got)
	}
	req.RemoteAddr = "not-a-socket"
	if got := clientIPFromRequest(req); got != "not-a-socket" {
		t.Fatalf("clientIPFromRequest raw = %q, want not-a-socket", got)
	}

	limiter := NewIPRateLimiter(0, 0)
	if limiter == nil || !limiter.Allow("", time.Unix(10, 0)) {
		t.Fatal("expected default-configured rate limiter to allow first request")
	}

	if got := durationString(45); got != "45s" {
		t.Fatalf("durationString(45) = %q, want 45s", got)
	}
}

func TestFetchAndEnsureDemoJWT(t *testing.T) {
	bootstrapClient := newTestHTTPClient(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("fetchDemoJWT method = %s, want POST", req.Method)
		}
		if req.URL.String() != "http://gateway:8080/api/users/login" {
			t.Fatalf("fetchDemoJWT url = %s", req.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"token":"bootstrap-jwt"}`)),
		}, nil
	})

	obsApp := newTestApp(t)
	obsApp.httpClient = bootstrapClient
	token, err := obsApp.fetchDemoJWT(context.Background())
	if err != nil || token != "bootstrap-jwt" {
		t.Fatalf("fetchDemoJWT returned (%q, %v)", token, err)
	}

	obsApp.httpClient = newTestHTTPClient(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{}`)),
		}, nil
	})
	if _, err := obsApp.fetchDemoJWT(context.Background()); err == nil {
		t.Fatal("expected fetchDemoJWT to fail on non-200 response")
	}

	obsApp.httpClient = newTestHTTPClient(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"token":""}`)),
		}, nil
	})
	if _, err := obsApp.fetchDemoJWT(context.Background()); err == nil {
		t.Fatal("expected fetchDemoJWT to fail on empty token")
	}

	t.Setenv(demoJWTEnvVar, "static-jwt")
	staticApp := newTestApp(t)
	if err := staticApp.ensureDemoJWT(context.Background()); err != nil {
		t.Fatalf("ensureDemoJWT static: %v", err)
	}
	if !staticApp.staticDemoJWT || staticApp.currentDemoJWT() != "static-jwt" {
		t.Fatalf("unexpected static demo jwt state: static=%v token=%q", staticApp.staticDemoJWT, staticApp.currentDemoJWT())
	}
	t.Setenv(demoJWTEnvVar, "")

	bootstrappedApp := newTestApp(t)
	bootstrappedApp.httpClient = bootstrapClient
	if err := bootstrappedApp.ensureDemoJWT(context.Background()); err != nil {
		t.Fatalf("ensureDemoJWT bootstrap: %v", err)
	}
	if got := bootstrappedApp.currentDemoJWT(); got != "bootstrap-jwt" {
		t.Fatalf("currentDemoJWT = %q, want bootstrap-jwt", got)
	}

	var bootstrapAttempts atomic.Int32
	retryingApp := newTestApp(t)
	retryingApp.httpClient = newTestHTTPClient(func(_ *http.Request) (*http.Response, error) {
		if bootstrapAttempts.Add(1) == 1 {
			return nil, errors.New("gateway warming up")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"token":"retried-bootstrap-jwt"}`)),
		}, nil
	})
	if err := retryingApp.ensureDemoJWT(context.Background()); err != nil {
		t.Fatalf("ensureDemoJWT retry bootstrap: %v", err)
	}
	if got := retryingApp.currentDemoJWT(); got != "retried-bootstrap-jwt" {
		t.Fatalf("retried currentDemoJWT = %q, want retried-bootstrap-jwt", got)
	}
	if got := bootstrapAttempts.Load(); got != 2 {
		t.Fatalf("bootstrap attempts = %d, want 2", got)
	}

	var nilApp *app
	if err := nilApp.ensureDemoJWT(context.Background()); err == nil {
		t.Fatal("expected nil app ensureDemoJWT to fail")
	}
}

func TestMetricsProxyHandlers(t *testing.T) {
	var gotURL string
	app := newTestApp(t)
	app.httpClient = newTestHTTPClient(func(req *http.Request) (*http.Response, error) {
		gotURL = req.URL.String()
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
		}, nil
	})

	req := httptest.NewRequest(http.MethodGet, "/api/metrics/query?query=gateway_requests_total", nil)
	req.RemoteAddr = "127.0.0.1:4321"
	recorder := httptest.NewRecorder()
	app.handleMetricsQuery(recorder, req)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("handleMetricsQuery status = %d, want %d", recorder.Code, http.StatusAccepted)
	}
	if gotURL != "http://prometheus:9090/api/v1/query?query=gateway_requests_total" {
		t.Fatalf("handleMetricsQuery upstream url = %q", gotURL)
	}

	rangeReq := httptest.NewRequest(http.MethodGet, "/api/metrics/query_range?query=sum(gateway_requests_total)", nil)
	rangeReq.RemoteAddr = "127.0.0.1:4321"
	rangeRecorder := httptest.NewRecorder()
	app.handleMetricsQueryRange(rangeRecorder, rangeReq)
	if rangeRecorder.Code != http.StatusAccepted {
		t.Fatalf("handleMetricsQueryRange status = %d, want %d", rangeRecorder.Code, http.StatusAccepted)
	}

	forbiddenReq := httptest.NewRequest(http.MethodGet, "/api/metrics/query?query=up", nil)
	forbiddenReq.RemoteAddr = "127.0.0.1:4321"
	forbiddenRecorder := httptest.NewRecorder()
	app.handleMetricsQuery(forbiddenRecorder, forbiddenReq)
	if forbiddenRecorder.Code != http.StatusForbidden {
		t.Fatalf("forbidden query status = %d, want %d", forbiddenRecorder.Code, http.StatusForbidden)
	}

	app.metricsLimiter = NewIPRateLimiter(1, time.Hour)
	limitedReq := httptest.NewRequest(http.MethodGet, "/api/metrics/query?query=gateway_requests_total", nil)
	limitedReq.RemoteAddr = "127.0.0.1:9999"
	app.handleMetricsQuery(httptest.NewRecorder(), limitedReq)
	limitedRecorder := httptest.NewRecorder()
	limitedReq = httptest.NewRequest(http.MethodGet, "/api/metrics/query?query=gateway_requests_total", nil)
	limitedReq.RemoteAddr = "127.0.0.1:9999"
	app.handleMetricsQuery(limitedRecorder, limitedReq)
	if limitedRecorder.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limited status = %d, want %d", limitedRecorder.Code, http.StatusTooManyRequests)
	}

	app.metricsLimiter = NewIPRateLimiter(1000, time.Minute)
	app.httpClient = newTestHTTPClient(func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("prometheus unavailable")
	})
	badGatewayReq := httptest.NewRequest(http.MethodGet, "/api/metrics/query?query=gateway_requests_total", nil)
	badGatewayReq.RemoteAddr = "127.0.0.1:4321"
	badGatewayRecorder := httptest.NewRecorder()
	app.handleMetricsQuery(badGatewayRecorder, badGatewayReq)
	if badGatewayRecorder.Code != http.StatusBadGateway {
		t.Fatalf("bad gateway status = %d, want %d", badGatewayRecorder.Code, http.StatusBadGateway)
	}
}

func TestResetAndChaosHelpers(t *testing.T) {
	var requestCount int32
	app := newTestApp(t)
	app.httpClient = newTestHTTPClient(func(req *http.Request) (*http.Response, error) {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		atomic.AddInt32(&requestCount, 1)
		switch {
		case req.URL.Path == "/health":
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`ok`))}, nil
		case req.URL.Path == "/admin/circuit-breakers/reset":
			if got := req.Header.Get("Authorization"); got != "Bearer "+app.adminToken {
				t.Fatalf("resetCircuitBreakers auth = %q", got)
			}
			return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(``))}, nil
		case req.URL.Path == "/chaos/reset":
			if got := req.Header.Get("Content-Type"); got != "application/json" {
				t.Fatalf("postJSON content-type = %q", got)
			}
			return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(``))}, nil
		default:
			t.Fatalf("unexpected request path %s", req.URL.Path)
			return nil, nil
		}
	})

	if err := app.waitForHealthy(context.Background(), serviceEndpoint{Name: "user-service-1", URL: "http://user-service-1"}); err != nil {
		t.Fatalf("waitForHealthy: %v", err)
	}
	if err := app.resetCircuitBreakers(context.Background()); err != nil {
		t.Fatalf("resetCircuitBreakers: %v", err)
	}
	if err := app.postJSON(context.Background(), "http://user-service-1/chaos/reset", map[string]string{"status": "ok"}); err != nil {
		t.Fatalf("postJSON: %v", err)
	}
	if requestCount < 3 {
		t.Fatalf("expected helper requests to run, got %d", requestCount)
	}

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := app.waitForHealthy(cancelCtx, serviceEndpoint{Name: "user-service-1", URL: "http://user-service-1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForHealthy canceled = %v, want %v", err, context.Canceled)
	}

	if url, err := serviceURL("user-service-1"); err != nil || !strings.Contains(url, "user-service-1") {
		t.Fatalf("serviceURL returned (%q, %v)", url, err)
	}
	if _, err := serviceURL("missing-service"); err == nil {
		t.Fatal("expected serviceURL to fail for unknown service")
	}
	if _, err := jsonMarshal(make(chan int)); err == nil {
		t.Fatal("expected jsonMarshal to fail on unsupported value")
	}

	app.active = &scenarioRun{name: "happy-path", cancel: func() {}}
	app.scenarioStatus["happy-path"] = statusRunning
	app.cancelActiveScenario()
	if got := app.scenarioStatusFor("happy-path"); got != statusStopping {
		t.Fatalf("cancelActiveScenario status = %q, want %q", got, statusStopping)
	}

	reset := partialReset("flush_redis", errors.New("boom"))
	if reset.Status != "partial" || reset.FailedStep != "flush_redis" || reset.Details != "boom" {
		t.Fatalf("unexpected partial reset payload: %#v", reset)
	}
}

func TestEnsureToxiproxy(t *testing.T) {
	app := newTestApp(t)
	var attempts atomic.Int32
	app.toxiproxy = NewToxiproxyClient(newTestHTTPClient(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/populate" {
			t.Fatalf("ensureToxiproxy path = %s, want /populate", req.URL.Path)
		}
		if attempts.Add(1) == 1 {
			return nil, errors.New("toxiproxy not ready")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`[]`)),
		}, nil
	}), newTestLogger())
	app.toxiproxy.baseURL = "http://toxiproxy:8474"

	if err := app.ensureToxiproxy(context.Background()); err != nil {
		t.Fatalf("ensureToxiproxy: %v", err)
	}
	if !app.toxiproxyReady {
		t.Fatal("expected ensureToxiproxy to mark client ready")
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("ensureToxiproxy attempts = %d, want 2", got)
	}
}

func TestRetryBootstrapReturnsContextErrorWhenDeadlineExpires(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := retryBootstrap(ctx, func(context.Context) error {
		return errors.New("still warming up")
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("retryBootstrap error = %v, want context deadline exceeded", err)
	}
}

func TestResolveHostProjectRootUsesWorkingDirectoryFallback(t *testing.T) {
	t.Setenv(projectRootEnvVar, "")
	current, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	got, err := resolveHostProjectRoot()
	if err != nil {
		t.Fatalf("resolveHostProjectRoot: %v", err)
	}
	if filepath.Clean(got) != filepath.Clean(current) {
		t.Fatalf("resolveHostProjectRoot = %q, want %q", got, current)
	}
}

func TestAppLifecycleHelpers(t *testing.T) {
	obsApp := newTestApp(t)
	obsApp.staticDemoJWT = true

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	obsApp.startBackgroundWorkers(ctx)
	obsApp.refreshDemoJWTLoop(ctx)
	obsApp.close()

	var nilApp *app
	nilApp.close()
}

func TestRunnerHelpers(t *testing.T) {
	runner := NewRunner(newTestLogger(), nil, "/tmp/project", "irongate")
	if runner == nil || runner.projectRoot != "/tmp/project" || runner.composeProject != "irongate" {
		t.Fatalf("unexpected runner: %#v", runner)
	}

	if err := runner.Wait(context.Background(), ""); err != nil {
		t.Fatalf("Wait empty container: %v", err)
	}
	if err := runner.Stop(context.Background(), ""); err != nil {
		t.Fatalf("Stop empty container: %v", err)
	}
}
