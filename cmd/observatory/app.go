package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/client"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	observatoryAddr        = ":9000"
	observatorySpecVersion = "2.2"

	demoTokenEnvVar             = "DEMO_TOKEN"
	adminTokenEnvVar            = "ADMIN_TOKEN"
	demoJWTEnvVar               = "DEMO_JWT"
	composeProjectEnvVar        = "COMPOSE_PROJECT_NAME"
	gatewayContainerNameEnvVar  = "GATEWAY_CONTAINER_NAME"
	projectRootEnvVar           = "PROJECT_ROOT"
	redisAddrEnvVar             = "REDIS_ADDR"
	defaultComposeProject       = "irongate"
	defaultRedisAddr            = "toxiproxy:6380"
	globalRateLimitPerMinute    = 100
	metricsRateLimitPerMinute   = 60
	rateLimitWindow             = time.Minute
	jwtBootstrapTimeout         = 10 * time.Second
	bootstrapAttemptTimeout     = 2 * time.Second
	bootstrapInitialBackoff     = 200 * time.Millisecond
	bootstrapMaxBackoff         = time.Second
	jwtRefreshInterval          = 23 * time.Hour
	requestTimeout              = 10 * time.Second
	k6ManagedContainerLabel     = "com.irongate.observatory.managed"
	k6ManagedContainerLabelTrue = "true"
	k6ScenarioLabel             = "com.irongate.observatory.scenario"
	observatoryRequestIDHeader  = "X-Request-ID"
)

type scenarioStatus string

const (
	statusIdle     scenarioStatus = "idle"
	statusRunning  scenarioStatus = "running"
	statusStopping scenarioStatus = "stopping"
	statusError    scenarioStatus = "error"
)

var (
	errScenarioAlreadyRunning = errors.New("another scenario is already running")
	errScenarioNotRunning     = errors.New("scenario is not running")
)

type healthResponse struct {
	Status         string `json:"status"`
	SpecVersion    string `json:"spec_version"`
	JWTValid       bool   `json:"jwt_valid"`
	ToxiproxyReady bool   `json:"toxiproxy_ready"`
}

type runParams struct {
	Intensity string `json:"intensity"`
	Duration  int    `json:"duration"`
}

type apiError struct {
	Error     string `json:"error"`
	Code      int    `json:"code"`
	RequestID string `json:"request_id"`
}

type scenarioRun struct {
	name        string
	containerID string
	cancel      context.CancelFunc
	once        sync.Once
}

type serviceEndpoint struct {
	Name string
	URL  string
}

var serviceEndpoints = []serviceEndpoint{
	{Name: "user-service-1", URL: "http://user-service-1:8081"},
	{Name: "user-service-2", URL: "http://user-service-2:8091"},
	{Name: "order-service-1", URL: "http://order-service-1:8082"},
	{Name: "order-service-2", URL: "http://order-service-2:8092"},
	{Name: "payment-service-1", URL: "http://payment-service-1:8083"},
}

type app struct {
	logger *slog.Logger

	docker dockerClient
	redis  rateLimitStore

	httpClient *http.Client

	demoToken            string
	adminToken           string
	composeProject       string
	gatewayContainerName string
	hostProjectRoot      string
	runtimeRoot          string
	staticDemoJWT        bool

	jwtMu   sync.RWMutex
	demoJWT string

	mu             sync.Mutex
	scenarios      map[string]*Scenario
	scenarioStatus map[string]scenarioStatus
	starting       *scenarioRun
	active         *scenarioRun

	eventHub       *EventHub
	globalLimiter  *IPRateLimiter
	metricsLimiter *IPRateLimiter
	runner         *Runner
	toxiproxy      *ToxiproxyClient

	toxiproxyReady bool
}

func newApp(ctx context.Context, logger *slog.Logger) (*app, error) {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	demoToken, err := requiredEnv(demoTokenEnvVar)
	if err != nil {
		return nil, err
	}
	adminToken, err := requiredEnv(adminTokenEnvVar)
	if err != nil {
		return nil, err
	}

	composeProject := strings.TrimSpace(os.Getenv(composeProjectEnvVar))
	if composeProject == "" {
		composeProject = defaultComposeProject
	}

	runtimeRoot, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	hostProjectRoot, err := resolveHostProjectRoot()
	if err != nil {
		return nil, err
	}

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}

	redisAddr := strings.TrimSpace(os.Getenv(redisAddrEnvVar))
	if redisAddr == "" {
		redisAddr = defaultRedisAddr
	}

	scenarios, err := loadScenarios(filepath.Join(runtimeRoot, "scenarios"))
	if err != nil {
		return nil, err
	}

	httpClient := &http.Client{Timeout: requestTimeout}
	app := &app{
		logger:               logger,
		docker:               dockerClient,
		redis:                newRedisRateLimitStore(redis.NewClient(&redis.Options{Addr: redisAddr})),
		httpClient:           httpClient,
		demoToken:            demoToken,
		adminToken:           adminToken,
		composeProject:       composeProject,
		gatewayContainerName: strings.TrimSpace(os.Getenv(gatewayContainerNameEnvVar)),
		hostProjectRoot:      hostProjectRoot,
		runtimeRoot:          runtimeRoot,
		scenarios:            scenarios,
		scenarioStatus:       make(map[string]scenarioStatus, len(scenarios)),
		eventHub:             NewEventHub(logger),
		globalLimiter:        NewIPRateLimiter(globalRateLimitPerMinute, rateLimitWindow),
		metricsLimiter:       NewIPRateLimiter(metricsRateLimitPerMinute, rateLimitWindow),
	}
	for name := range scenarios {
		app.scenarioStatus[name] = statusIdle
	}

	app.runner = NewRunner(logger, dockerClient, hostProjectRoot, composeProject)
	app.toxiproxy = NewToxiproxyClient(httpClient, logger)

	if err := app.ensureDemoJWT(ctx); err != nil {
		return nil, err
	}
	if err := app.ensureToxiproxy(ctx); err != nil {
		return nil, err
	}

	return app, nil
}

func (a *app) close() {
	if a == nil {
		return
	}

	a.cancelActiveScenario()
	if a.runner != nil {
		_ = a.runner.StopManagedContainers(context.Background())
	}
	if a.docker != nil && a.httpClient != nil {
		_ = a.resetChaos(context.Background())
	}
	if a.redis != nil {
		_ = a.redis.Close()
	}
	if a.docker != nil {
		_ = a.docker.Close()
	}
}

func (a *app) startBackgroundWorkers(ctx context.Context) {
	if a == nil {
		return
	}

	if !a.staticDemoJWT {
		go a.refreshDemoJWTLoop(ctx)
	}
	go a.streamGatewayEvents(ctx)
}

func (a *app) ensureDemoJWT(ctx context.Context) error {
	if a == nil {
		return errors.New("app is nil")
	}

	staticJWT := strings.TrimSpace(os.Getenv(demoJWTEnvVar))
	if staticJWT != "" {
		a.staticDemoJWT = true
		a.setDemoJWT(staticJWT)
		a.logger.Info("Using static DEMO_JWT")
		return nil
	}

	bootstrapCtx, cancel := context.WithTimeout(ctx, jwtBootstrapTimeout)
	defer cancel()

	var token string
	if err := retryBootstrap(bootstrapCtx, func(attemptCtx context.Context) error {
		fetched, err := a.fetchDemoJWT(attemptCtx)
		if err != nil {
			return err
		}
		token = fetched
		return nil
	}); err != nil {
		return fmt.Errorf("bootstrap demo jwt: %w", err)
	}

	a.setDemoJWT(token)
	return nil
}

func (a *app) refreshDemoJWTLoop(ctx context.Context) {
	ticker := time.NewTicker(jwtRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshCtx, cancel := context.WithTimeout(ctx, jwtBootstrapTimeout)
			token, err := a.fetchDemoJWT(refreshCtx)
			cancel()
			if err != nil {
				a.logger.Warn("failed to refresh demo jwt", "error", err)
				continue
			}
			a.setDemoJWT(token)
		}
	}
}

func (a *app) fetchDemoJWT(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://gateway:8080/api/users/login", bytes.NewReader(nil))
	if err != nil {
		return "", fmt.Errorf("build jwt bootstrap request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("bootstrap login request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bootstrap login returned status %d", resp.StatusCode)
	}

	var payload struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode bootstrap login response: %w", err)
	}
	if strings.TrimSpace(payload.Token) == "" {
		return "", errors.New("bootstrap login returned empty token")
	}

	return payload.Token, nil
}

func retryBootstrap(ctx context.Context, fn func(context.Context) error) error {
	backoff := bootstrapInitialBackoff
	var lastErr error

	for {
		attemptCtx, cancel := context.WithTimeout(ctx, bootstrapAttemptTimeout)
		err := fn(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
		if !sleepContext(ctx, backoff) {
			break
		}
		if backoff < bootstrapMaxBackoff {
			backoff *= 2
			if backoff > bootstrapMaxBackoff {
				backoff = bootstrapMaxBackoff
			}
		}
	}

	if lastErr == nil {
		return ctx.Err()
	}
	if ctx.Err() != nil {
		return fmt.Errorf("%w: %v", ctx.Err(), lastErr)
	}

	return lastErr
}

func (a *app) currentDemoJWT() string {
	a.jwtMu.RLock()
	defer a.jwtMu.RUnlock()
	return a.demoJWT
}

func (a *app) setDemoJWT(token string) {
	a.jwtMu.Lock()
	defer a.jwtMu.Unlock()
	a.demoJWT = strings.TrimSpace(token)
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", a.handleHealth)
	mux.HandleFunc("GET /api/scenarios", a.handleListScenarios)
	mux.HandleFunc("GET /api/scenarios/{name}", a.handleGetScenario)
	mux.HandleFunc("GET /api/scenarios/{name}/status", a.handleScenarioStatus)
	mux.HandleFunc("POST /api/scenarios/{name}/run", a.withMutationAuth(a.handleRunScenario))
	mux.HandleFunc("POST /api/scenarios/{name}/stop", a.withMutationAuth(a.handleStopScenario))
	mux.HandleFunc("GET /api/events", a.handleEvents)
	mux.HandleFunc("GET /api/metrics/query", a.handleMetricsQuery)
	mux.HandleFunc("GET /api/metrics/query_range", a.handleMetricsQueryRange)
	mux.HandleFunc("POST /api/reset", a.withMutationAuth(a.handleReset))

	return withRequestID(a.withGlobalLimit(mux))
}

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get(observatoryRequestIDHeader))
		if requestID == "" {
			requestID = uuid.NewString()
		}

		r.Header.Set(observatoryRequestIDHeader, requestID)
		w.Header().Set(observatoryRequestIDHeader, requestID)
		next.ServeHTTP(w, r)
	})
}

func (a *app) withGlobalLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.globalLimiter.Allow(clientIPFromRequest(r), time.Now()) {
			writeAPIError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (a *app) withMutationAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authorizedBearerToken(r, a.demoToken) {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		next.ServeHTTP(w, r)
	}
}

func (a *app) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{
		Status:         "ok",
		SpecVersion:    observatorySpecVersion,
		JWTValid:       strings.TrimSpace(a.currentDemoJWT()) != "",
		ToxiproxyReady: a.toxiproxyReady,
	})
}

func (a *app) scenarioStatusFor(name string) scenarioStatus {
	a.mu.Lock()
	defer a.mu.Unlock()

	status, ok := a.scenarioStatus[name]
	if !ok {
		return statusIdle
	}

	return status
}

func (a *app) setScenarioStatusLocked(name string, status scenarioStatus) {
	a.scenarioStatus[name] = status
}

func (a *app) startScenario(scenario *Scenario, params runParams) error {
	intensity, durationSeconds, err := scenario.ResolveRun(params)
	if err != nil {
		return err
	}

	a.mu.Lock()
	if a.active != nil || a.starting != nil {
		a.mu.Unlock()
		return errScenarioAlreadyRunning
	}
	runCtx, cancel := context.WithCancel(context.Background())
	run := &scenarioRun{name: scenario.Name, cancel: cancel}
	a.starting = run
	a.setScenarioStatusLocked(scenario.Name, statusRunning)
	a.mu.Unlock()

	containerID, err := a.runner.Start(runCtx, scenario, strings.TrimSpace(params.Intensity), intensity.RPS, durationSeconds, a.currentDemoJWT())
	a.mu.Lock()
	if a.starting == run {
		a.starting = nil
	}
	if err != nil {
		if errors.Is(err, context.Canceled) && a.scenarioStatus[scenario.Name] == statusStopping {
			a.setScenarioStatusLocked(scenario.Name, statusIdle)
		} else {
			a.setScenarioStatusLocked(scenario.Name, statusError)
		}
		a.mu.Unlock()
		cancel()
		return err
	}
	run.containerID = containerID
	if runCtx.Err() != nil {
		a.mu.Unlock()
		_ = a.runner.Stop(context.Background(), containerID)
		a.completeScenario(run, scenario.Name, nil)
		return context.Canceled
	}
	a.active = run
	a.mu.Unlock()

	a.eventHub.Publish(SystemEvent("scenario_started", "scenario started", map[string]any{
		"scenario":  scenario.Name,
		"intensity": params.Intensity,
		"duration":  durationSeconds,
		"rps":       intensity.RPS,
	}))

	go a.watchScenario(runCtx, run, scenario)
	return nil
}

func (a *app) watchScenario(ctx context.Context, run *scenarioRun, scenario *Scenario) {
	chaosErrCh := make(chan error, 1)
	go func() {
		chaosErrCh <- a.executeChaos(ctx, scenario)
	}()

	waitErr := a.runner.Wait(ctx, run.containerID)
	chaosErr := <-chaosErrCh

	if waitErr != nil && !errors.Is(waitErr, context.Canceled) {
		a.completeScenario(run, scenario.Name, waitErr)
		return
	}
	if chaosErr != nil && !errors.Is(chaosErr, context.Canceled) {
		a.completeScenario(run, scenario.Name, chaosErr)
		return
	}

	a.completeScenario(run, scenario.Name, nil)
}

func (a *app) stopScenario(ctx context.Context, name string) error {
	a.mu.Lock()
	run := a.active
	if run == nil {
		run = a.starting
	}
	if run == nil || run.name != name {
		a.mu.Unlock()
		return errScenarioNotRunning
	}
	a.setScenarioStatusLocked(name, statusStopping)
	containerID := run.containerID
	a.mu.Unlock()

	run.cancel()
	return a.runner.Stop(ctx, containerID)
}

func (a *app) completeScenario(run *scenarioRun, name string, err error) {
	run.once.Do(func() {
		a.mu.Lock()
		defer a.mu.Unlock()

		if a.active == run {
			a.active = nil
		}
		if a.starting == run {
			a.starting = nil
		}
		if err != nil {
			a.setScenarioStatusLocked(name, statusError)
			a.logger.Error("scenario run failed", "scenario", name, "error", err)
		} else {
			a.setScenarioStatusLocked(name, statusIdle)
		}

		a.eventHub.Publish(SystemEvent("scenario_stopped", "scenario stopped", map[string]any{
			"scenario": name,
			"status":   a.scenarioStatus[name],
		}))
	})
}

func authorizedBearerToken(req *http.Request, expected string) bool {
	if req == nil {
		return false
	}

	authValue := strings.TrimSpace(req.Header.Get("Authorization"))
	scheme, token, ok := strings.Cut(authValue, " ")
	if !ok || !strings.EqualFold(strings.TrimSpace(scheme), "Bearer") {
		return false
	}

	token = strings.TrimSpace(token)
	expected = strings.TrimSpace(expected)
	if token == "" || expected == "" {
		return false
	}

	return hmac.Equal([]byte(token), []byte(expected))
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, apiError{
		Error:     message,
		Code:      status,
		RequestID: responseRequestID(w),
	})
}

func responseRequestID(w http.ResponseWriter) string {
	if w == nil {
		return uuid.NewString()
	}

	requestID := strings.TrimSpace(w.Header().Get(observatoryRequestIDHeader))
	if requestID == "" {
		requestID = uuid.NewString()
		w.Header().Set(observatoryRequestIDHeader, requestID)
	}

	return requestID
}

func requiredEnv(key string) (string, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return "", fmt.Errorf("%s must be set", key)
	}

	return value, nil
}

func resolveHostProjectRoot() (string, error) {
	if value := strings.TrimSpace(os.Getenv(projectRootEnvVar)); value != "" {
		return value, nil
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve host project root: %w", err)
	}

	return wd, nil
}

func clientIPFromRequest(req *http.Request) string {
	if req == nil {
		return "unknown"
	}

	host, _, err := net.SplitHostPort(strings.TrimSpace(req.RemoteAddr))
	if err != nil {
		return strings.TrimSpace(req.RemoteAddr)
	}

	if strings.TrimSpace(host) == "" {
		return "unknown"
	}

	return host
}

type ipRateState struct {
	tokens     float64
	lastRefill time.Time
}

type IPRateLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	rate     float64
	capacity float64
	state    map[string]ipRateState
}

func NewIPRateLimiter(limit int, window time.Duration) *IPRateLimiter {
	if limit <= 0 {
		limit = 1
	}
	if window <= 0 {
		window = time.Minute
	}

	return &IPRateLimiter{
		limit:    limit,
		window:   window,
		rate:     float64(limit) / window.Seconds(),
		capacity: float64(limit),
		state:    make(map[string]ipRateState),
	}
}

func (l *IPRateLimiter) Allow(key string, now time.Time) bool {
	if l == nil {
		return true
	}
	if now.IsZero() {
		now = time.Now()
	}

	if strings.TrimSpace(key) == "" {
		key = "unknown"
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	current, ok := l.state[key]
	if !ok {
		l.state[key] = ipRateState{
			tokens:     l.capacity - 1,
			lastRefill: now,
		}
		return true
	}

	elapsed := now.Sub(current.lastRefill).Seconds()
	current.tokens += elapsed * l.rate
	if current.tokens > l.capacity {
		current.tokens = l.capacity
	}
	current.lastRefill = now

	if current.tokens < 1 {
		l.state[key] = current
		return false
	}

	current.tokens--
	l.state[key] = current
	return true
}

func SystemEvent(typ, message string, attrs map[string]any) Event {
	return Event{
		TS:      time.Now().UTC(),
		Level:   "info",
		Type:    typ,
		Message: message,
		Attrs:   attrs,
	}
}

func durationString(seconds int) string {
	return strconv.Itoa(seconds) + "s"
}
