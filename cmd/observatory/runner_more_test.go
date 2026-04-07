package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	dockerfilters "github.com/docker/docker/api/types/filters"
	dockerimage "github.com/docker/docker/api/types/image"
	dockernetwork "github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type stopCall struct {
	id      string
	options dockercontainer.StopOptions
}

type removeCall struct {
	id      string
	options dockercontainer.RemoveOptions
}

type createCall struct {
	config        *dockercontainer.Config
	hostConfig    *dockercontainer.HostConfig
	networkConfig *dockernetwork.NetworkingConfig
	platform      *ocispec.Platform
	name          string
}

type mockDockerClient struct {
	mu sync.Mutex

	containerCreateFunc  func(ctx context.Context, config *dockercontainer.Config, hostConfig *dockercontainer.HostConfig, networkingConfig *dockernetwork.NetworkingConfig, platform *ocispec.Platform, containerName string) (dockercontainer.CreateResponse, error)
	containerInspectFunc func(ctx context.Context, containerID string) (dockercontainer.InspectResponse, error)
	containerListFunc    func(ctx context.Context, options dockercontainer.ListOptions) ([]dockercontainer.Summary, error)
	containerLogsFunc    func(ctx context.Context, containerID string, options dockercontainer.LogsOptions) (io.ReadCloser, error)
	containerRemoveFunc  func(ctx context.Context, containerID string, options dockercontainer.RemoveOptions) error
	containerStartFunc   func(ctx context.Context, containerID string, options dockercontainer.StartOptions) error
	containerStopFunc    func(ctx context.Context, containerID string, options dockercontainer.StopOptions) error
	containerWaitFunc    func(ctx context.Context, containerID string, condition dockercontainer.WaitCondition) (<-chan dockercontainer.WaitResponse, <-chan error)
	imageInspectFunc     func(ctx context.Context, imageID string) (dockerimage.InspectResponse, []byte, error)
	imagePullFunc        func(ctx context.Context, ref string, options dockerimage.PullOptions) (io.ReadCloser, error)
	closeFunc            func() error

	createCalls   []createCall
	inspectIDs    []string
	listCalls     []dockercontainer.ListOptions
	logIDs        []string
	startedIDs    []string
	stoppedCalls  []stopCall
	removedCalls  []removeCall
	waitIDs       []string
	imagePullRefs []string
	closeCalls    int
}

func (m *mockDockerClient) ContainerCreate(ctx context.Context, config *dockercontainer.Config, hostConfig *dockercontainer.HostConfig, networkingConfig *dockernetwork.NetworkingConfig, platform *ocispec.Platform, containerName string) (dockercontainer.CreateResponse, error) {
	m.mu.Lock()
	m.createCalls = append(m.createCalls, createCall{
		config:        config,
		hostConfig:    hostConfig,
		networkConfig: networkingConfig,
		platform:      platform,
		name:          containerName,
	})
	fn := m.containerCreateFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, config, hostConfig, networkingConfig, platform, containerName)
	}

	return dockercontainer.CreateResponse{ID: "k6-container"}, nil
}

func (m *mockDockerClient) ContainerInspect(ctx context.Context, containerID string) (dockercontainer.InspectResponse, error) {
	m.mu.Lock()
	m.inspectIDs = append(m.inspectIDs, containerID)
	fn := m.containerInspectFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, containerID)
	}

	return dockercontainer.InspectResponse{ContainerJSONBase: &dockercontainer.ContainerJSONBase{ID: containerID}}, nil
}

func (m *mockDockerClient) ContainerList(ctx context.Context, options dockercontainer.ListOptions) ([]dockercontainer.Summary, error) {
	m.mu.Lock()
	m.listCalls = append(m.listCalls, options)
	fn := m.containerListFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, options)
	}

	return nil, nil
}

func (m *mockDockerClient) ContainerLogs(ctx context.Context, containerID string, options dockercontainer.LogsOptions) (io.ReadCloser, error) {
	m.mu.Lock()
	m.logIDs = append(m.logIDs, containerID)
	fn := m.containerLogsFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, containerID, options)
	}

	return io.NopCloser(strings.NewReader("")), nil
}

func (m *mockDockerClient) ContainerRemove(ctx context.Context, containerID string, options dockercontainer.RemoveOptions) error {
	m.mu.Lock()
	m.removedCalls = append(m.removedCalls, removeCall{id: containerID, options: options})
	fn := m.containerRemoveFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, containerID, options)
	}

	return nil
}

func (m *mockDockerClient) ContainerStart(ctx context.Context, containerID string, options dockercontainer.StartOptions) error {
	m.mu.Lock()
	m.startedIDs = append(m.startedIDs, containerID)
	fn := m.containerStartFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, containerID, options)
	}

	return nil
}

func (m *mockDockerClient) ContainerStop(ctx context.Context, containerID string, options dockercontainer.StopOptions) error {
	m.mu.Lock()
	m.stoppedCalls = append(m.stoppedCalls, stopCall{id: containerID, options: options})
	fn := m.containerStopFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, containerID, options)
	}

	return nil
}

func (m *mockDockerClient) ContainerWait(ctx context.Context, containerID string, condition dockercontainer.WaitCondition) (<-chan dockercontainer.WaitResponse, <-chan error) {
	m.mu.Lock()
	m.waitIDs = append(m.waitIDs, containerID)
	fn := m.containerWaitFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, containerID, condition)
	}

	statusCh := make(chan dockercontainer.WaitResponse, 1)
	errCh := make(chan error, 1)
	statusCh <- dockercontainer.WaitResponse{StatusCode: 0}
	close(statusCh)
	close(errCh)
	return statusCh, errCh
}

func (m *mockDockerClient) ImageInspectWithRaw(ctx context.Context, imageID string) (dockerimage.InspectResponse, []byte, error) {
	m.mu.Lock()
	fn := m.imageInspectFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, imageID)
	}

	return dockerimage.InspectResponse{}, nil, nil
}

func (m *mockDockerClient) ImagePull(ctx context.Context, ref string, options dockerimage.PullOptions) (io.ReadCloser, error) {
	m.mu.Lock()
	m.imagePullRefs = append(m.imagePullRefs, ref)
	fn := m.imagePullFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, ref, options)
	}

	return io.NopCloser(strings.NewReader("{}")), nil
}

func (m *mockDockerClient) Close() error {
	m.mu.Lock()
	m.closeCalls++
	fn := m.closeFunc
	m.mu.Unlock()

	if fn != nil {
		return fn()
	}

	return nil
}

type mockScanResult struct {
	keys   []string
	cursor uint64
	err    error
}

type mockRateLimitStore struct {
	mu        sync.Mutex
	scanCalls []struct {
		cursor uint64
		match  string
		count  int64
	}
	scanResults []mockScanResult
	deleted     [][]string
	closeCalls  int
}

func (m *mockRateLimitStore) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeCalls++
	return nil
}

func (m *mockRateLimitStore) Del(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted = append(m.deleted, append([]string(nil), keys...))
	return nil
}

func (m *mockRateLimitStore) Scan(_ context.Context, cursor uint64, match string, count int64) ([]string, uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.scanCalls = append(m.scanCalls, struct {
		cursor uint64
		match  string
		count  int64
	}{cursor: cursor, match: match, count: count})

	if len(m.scanResults) == 0 {
		return nil, 0, nil
	}

	result := m.scanResults[0]
	m.scanResults = m.scanResults[1:]
	return append([]string(nil), result.keys...), result.cursor, result.err
}

func serviceLabelFromFilters(args dockerfilters.Args) string {
	for _, label := range args.Get("label") {
		if strings.HasPrefix(label, "com.docker.compose.service=") {
			return strings.TrimPrefix(label, "com.docker.compose.service=")
		}
	}

	return ""
}

func hasManagedContainerLabel(args dockerfilters.Args) bool {
	for _, label := range args.Get("label") {
		if label == k6ManagedContainerLabel+"="+k6ManagedContainerLabelTrue {
			return true
		}
	}

	return false
}

func waitForStartingRun(t *testing.T, app *app) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		app.mu.Lock()
		starting := app.starting
		app.mu.Unlock()
		if starting != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("timed out waiting for starting scenario")
}

func TestRunnerStartBuildsK6ContainerConfig(t *testing.T) {
	docker := &mockDockerClient{
		containerCreateFunc: func(context.Context, *dockercontainer.Config, *dockercontainer.HostConfig, *dockernetwork.NetworkingConfig, *ocispec.Platform, string) (dockercontainer.CreateResponse, error) {
			return dockercontainer.CreateResponse{ID: "k6-123"}, nil
		},
	}
	runner := NewRunner(newTestLogger(), docker, "/tmp/project", "irongate")

	containerID, err := runner.Start(context.Background(), &Scenario{
		Name:     "checkout",
		K6Script: "scenarios/k6/checkout.js",
	}, 42, 90, "demo-jwt")
	if err != nil {
		t.Fatalf("Runner.Start: %v", err)
	}
	if containerID != "k6-123" {
		t.Fatalf("containerID = %q, want k6-123", containerID)
	}

	docker.mu.Lock()
	defer docker.mu.Unlock()

	if len(docker.createCalls) != 1 {
		t.Fatalf("create calls = %d, want 1", len(docker.createCalls))
	}
	createCall := docker.createCalls[0]
	if createCall.config.Image != k6ImageRef {
		t.Fatalf("container image = %q, want %q", createCall.config.Image, k6ImageRef)
	}
	if got := strings.Join(createCall.config.Cmd, " "); got != "run /scripts/checkout.js" {
		t.Fatalf("container cmd = %q, want %q", got, "run /scripts/checkout.js")
	}
	if createCall.config.Labels[k6ManagedContainerLabel] != k6ManagedContainerLabelTrue {
		t.Fatalf("managed label = %q, want %q", createCall.config.Labels[k6ManagedContainerLabel], k6ManagedContainerLabelTrue)
	}
	if createCall.config.Labels[k6ScenarioLabel] != "checkout" {
		t.Fatalf("scenario label = %q, want checkout", createCall.config.Labels[k6ScenarioLabel])
	}
	if createCall.hostConfig.NetworkMode != dockercontainer.NetworkMode("irongate_default") {
		t.Fatalf("network mode = %q, want %q", createCall.hostConfig.NetworkMode, "irongate_default")
	}
	if len(createCall.hostConfig.Binds) != 1 || createCall.hostConfig.Binds[0] != "/tmp/project/scenarios/k6:/scripts:ro" {
		t.Fatalf("binds = %#v", createCall.hostConfig.Binds)
	}
	if createCall.name == "" || !strings.HasPrefix(createCall.name, "irongate-k6-checkout-") {
		t.Fatalf("container name = %q, want irongate-k6-checkout-*", createCall.name)
	}

	wantEnv := map[string]bool{
		"RPS=42":                                true,
		"DURATION=90":                           true,
		"TARGET_URL=http://gateway:8080":        true,
		"JWT=demo-jwt":                          true,
		"TOKEN_POOL_SIZE=42":                    true,
		"LOGIN_SUBJECT_PREFIX=observatory-user": true,
		"LOGIN_ROLE=admin":                      true,
	}
	for _, env := range createCall.config.Env {
		delete(wantEnv, env)
	}
	if len(wantEnv) != 0 {
		t.Fatalf("missing env vars: %#v", wantEnv)
	}
	if len(docker.startedIDs) != 1 || docker.startedIDs[0] != "k6-123" {
		t.Fatalf("started IDs = %#v, want [k6-123]", docker.startedIDs)
	}
}

func TestRunnerStopStopsAndRemovesContainer(t *testing.T) {
	docker := &mockDockerClient{}
	runner := NewRunner(newTestLogger(), docker, "/tmp/project", "irongate")

	if err := runner.Stop(context.Background(), "k6-123"); err != nil {
		t.Fatalf("Runner.Stop: %v", err)
	}

	docker.mu.Lock()
	defer docker.mu.Unlock()

	if len(docker.stoppedCalls) != 1 || docker.stoppedCalls[0].id != "k6-123" {
		t.Fatalf("stop calls = %#v", docker.stoppedCalls)
	}
	if docker.stoppedCalls[0].options.Timeout == nil || *docker.stoppedCalls[0].options.Timeout != 5 {
		t.Fatalf("stop timeout = %#v, want 5", docker.stoppedCalls[0].options.Timeout)
	}
	if len(docker.removedCalls) != 1 || docker.removedCalls[0].id != "k6-123" {
		t.Fatalf("remove calls = %#v", docker.removedCalls)
	}
	if !docker.removedCalls[0].options.Force || !docker.removedCalls[0].options.RemoveVolumes {
		t.Fatalf("remove options = %#v, want force+remove volumes", docker.removedCalls[0].options)
	}
}

func TestRunnerStopManagedContainersUsesManagedLabelFilter(t *testing.T) {
	t.Run("empty list", func(t *testing.T) {
		docker := &mockDockerClient{}
		runner := NewRunner(newTestLogger(), docker, "/tmp/project", "irongate")

		if err := runner.StopManagedContainers(context.Background()); err != nil {
			t.Fatalf("StopManagedContainers: %v", err)
		}

		docker.mu.Lock()
		defer docker.mu.Unlock()

		if len(docker.listCalls) != 1 {
			t.Fatalf("list calls = %d, want 1", len(docker.listCalls))
		}
		if !hasManagedContainerLabel(docker.listCalls[0].Filters) {
			t.Fatalf("expected managed label filter, got %#v", docker.listCalls[0].Filters.Get("label"))
		}
	})

	t.Run("stops listed containers", func(t *testing.T) {
		docker := &mockDockerClient{
			containerListFunc: func(context.Context, dockercontainer.ListOptions) ([]dockercontainer.Summary, error) {
				return []dockercontainer.Summary{{ID: "k6-1"}}, nil
			},
		}
		runner := NewRunner(newTestLogger(), docker, "/tmp/project", "irongate")

		if err := runner.StopManagedContainers(context.Background()); err != nil {
			t.Fatalf("StopManagedContainers: %v", err)
		}

		docker.mu.Lock()
		defer docker.mu.Unlock()

		if len(docker.stoppedCalls) != 1 || docker.stoppedCalls[0].id != "k6-1" {
			t.Fatalf("stop calls = %#v", docker.stoppedCalls)
		}
		if len(docker.removedCalls) != 1 || docker.removedCalls[0].id != "k6-1" {
			t.Fatalf("remove calls = %#v", docker.removedCalls)
		}
	})
}

func TestRunnerWaitRemovesContainerOnSuccess(t *testing.T) {
	docker := &mockDockerClient{
		containerWaitFunc: func(context.Context, string, dockercontainer.WaitCondition) (<-chan dockercontainer.WaitResponse, <-chan error) {
			statusCh := make(chan dockercontainer.WaitResponse, 1)
			errCh := make(chan error, 1)
			statusCh <- dockercontainer.WaitResponse{StatusCode: 0}
			close(statusCh)
			close(errCh)
			return statusCh, errCh
		},
	}
	runner := NewRunner(newTestLogger(), docker, "/tmp/project", "irongate")

	if err := runner.Wait(context.Background(), "k6-wait"); err != nil {
		t.Fatalf("Runner.Wait: %v", err)
	}

	docker.mu.Lock()
	defer docker.mu.Unlock()

	if len(docker.waitIDs) != 1 || docker.waitIDs[0] != "k6-wait" {
		t.Fatalf("wait IDs = %#v", docker.waitIDs)
	}
	if len(docker.removedCalls) != 1 || docker.removedCalls[0].id != "k6-wait" {
		t.Fatalf("remove calls = %#v", docker.removedCalls)
	}
}

func TestRunnerEnsureImagePullsWhenMissing(t *testing.T) {
	docker := &mockDockerClient{
		imageInspectFunc: func(context.Context, string) (dockerimage.InspectResponse, []byte, error) {
			return dockerimage.InspectResponse{}, nil, errors.New("missing image")
		},
	}
	runner := NewRunner(newTestLogger(), docker, "/tmp/project", "irongate")

	if err := runner.ensureImage(context.Background()); err != nil {
		t.Fatalf("ensureImage: %v", err)
	}

	docker.mu.Lock()
	defer docker.mu.Unlock()

	if len(docker.imagePullRefs) != 1 || docker.imagePullRefs[0] != k6ImageRef {
		t.Fatalf("image pull refs = %#v, want [%q]", docker.imagePullRefs, k6ImageRef)
	}
}

func TestExecuteChaosStepUsesServiceContainerIDs(t *testing.T) {
	docker := &mockDockerClient{
		containerListFunc: func(_ context.Context, options dockercontainer.ListOptions) ([]dockercontainer.Summary, error) {
			switch serviceLabelFromFilters(options.Filters) {
			case "user-service-1":
				return []dockercontainer.Summary{{ID: "user-1", State: "running"}}, nil
			case "order-service-1":
				return []dockercontainer.Summary{{ID: "order-1", State: "exited"}}, nil
			default:
				return nil, errors.New("unexpected service lookup")
			}
		},
	}
	app := newTestApp(t)
	app.composeProject = "irongate"
	app.docker = docker
	app.httpClient = newTestHTTPClient(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/health" {
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})

	if err := app.executeChaosStep(context.Background(), ChaosStep{Action: "service_down", Target: "user-service-1"}); err != nil {
		t.Fatalf("executeChaosStep(service_down): %v", err)
	}
	if err := app.executeChaosStep(context.Background(), ChaosStep{Action: "service_up", Target: "order-service-1"}); err != nil {
		t.Fatalf("executeChaosStep(service_up): %v", err)
	}

	docker.mu.Lock()
	defer docker.mu.Unlock()

	if len(docker.stoppedCalls) != 1 || docker.stoppedCalls[0].id != "user-1" {
		t.Fatalf("stop calls = %#v", docker.stoppedCalls)
	}
	if len(docker.startedIDs) != 1 || docker.startedIDs[0] != "order-1" {
		t.Fatalf("start calls = %#v", docker.startedIDs)
	}
}

func TestGatewayContainerIDPrefersOverrideAndFallsBackToComposeLookup(t *testing.T) {
	t.Run("prefers override", func(t *testing.T) {
		docker := &mockDockerClient{
			containerInspectFunc: func(context.Context, string) (dockercontainer.InspectResponse, error) {
				return dockercontainer.InspectResponse{
					ContainerJSONBase: &dockercontainer.ContainerJSONBase{ID: "gateway-explicit-id"},
				}, nil
			},
		}
		app := newTestApp(t)
		app.docker = docker
		app.gatewayContainerName = "gateway-explicit"

		containerID, err := app.gatewayContainerID(context.Background())
		if err != nil {
			t.Fatalf("gatewayContainerID override: %v", err)
		}
		if containerID != "gateway-explicit-id" {
			t.Fatalf("containerID = %q, want gateway-explicit-id", containerID)
		}
	})

	t.Run("falls back to compose labels", func(t *testing.T) {
		docker := &mockDockerClient{
			containerInspectFunc: func(context.Context, string) (dockercontainer.InspectResponse, error) {
				return dockercontainer.InspectResponse{}, errors.New("not found")
			},
			containerListFunc: func(context.Context, dockercontainer.ListOptions) ([]dockercontainer.Summary, error) {
				return []dockercontainer.Summary{{ID: "gateway-compose-id"}}, nil
			},
		}
		app := newTestApp(t)
		app.composeProject = "irongate"
		app.docker = docker
		app.gatewayContainerName = "gateway-explicit"

		containerID, err := app.gatewayContainerID(context.Background())
		if err != nil {
			t.Fatalf("gatewayContainerID fallback: %v", err)
		}
		if containerID != "gateway-compose-id" {
			t.Fatalf("containerID = %q, want gateway-compose-id", containerID)
		}
	})
}

func TestStartScenarioClearsCanceledStartup(t *testing.T) {
	docker := &mockDockerClient{
		containerCreateFunc: func(context.Context, *dockercontainer.Config, *dockercontainer.HostConfig, *dockernetwork.NetworkingConfig, *ocispec.Platform, string) (dockercontainer.CreateResponse, error) {
			return dockercontainer.CreateResponse{ID: "k6-starting"}, nil
		},
		containerStartFunc: func(ctx context.Context, _ string, _ dockercontainer.StartOptions) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	app := newTestApp(t)
	app.docker = docker
	app.runner = NewRunner(app.logger, docker, "/tmp/project", "irongate")

	errCh := make(chan error, 1)
	go func() {
		errCh <- app.startScenario(app.scenarios["happy-path"], runParams{Intensity: "mild", Duration: 30})
	}()

	waitForStartingRun(t, app)

	if err := app.stopScenario(context.Background(), "happy-path"); err != nil {
		t.Fatalf("stopScenario during startup: %v", err)
	}
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("startScenario error = %v, want context canceled", err)
	}

	app.mu.Lock()
	defer app.mu.Unlock()

	if app.starting != nil || app.active != nil {
		t.Fatalf("expected no scenario pointers after canceled startup, got starting=%#v active=%#v", app.starting, app.active)
	}
	if app.scenarioStatus["happy-path"] != statusIdle {
		t.Fatalf("scenario status = %q, want %q", app.scenarioStatus["happy-path"], statusIdle)
	}
}

func TestWatchScenarioCompletesSuccessfulRun(t *testing.T) {
	docker := &mockDockerClient{
		containerWaitFunc: func(context.Context, string, dockercontainer.WaitCondition) (<-chan dockercontainer.WaitResponse, <-chan error) {
			statusCh := make(chan dockercontainer.WaitResponse, 1)
			errCh := make(chan error, 1)
			statusCh <- dockercontainer.WaitResponse{StatusCode: 0}
			close(statusCh)
			close(errCh)
			return statusCh, errCh
		},
	}
	app := newTestApp(t)
	app.runner = NewRunner(app.logger, docker, "/tmp/project", "irongate")

	run := &scenarioRun{name: "happy-path", containerID: "k6-watch"}
	app.active = run
	app.scenarioStatus["happy-path"] = statusRunning

	app.watchScenario(context.Background(), run, app.scenarios["happy-path"])

	if app.active != nil {
		t.Fatal("expected watchScenario to clear the active run")
	}
	if got := app.scenarioStatusFor("happy-path"); got != statusIdle {
		t.Fatalf("scenario status = %q, want %q", got, statusIdle)
	}
}

func TestResetSystemRunsCleanupSequence(t *testing.T) {
	previousDrainDelay := resetDrainDelay
	resetDrainDelay = time.Millisecond
	t.Cleanup(func() {
		resetDrainDelay = previousDrainDelay
	})

	docker := &mockDockerClient{
		containerListFunc: func(_ context.Context, options dockercontainer.ListOptions) ([]dockercontainer.Summary, error) {
			if hasManagedContainerLabel(options.Filters) {
				return []dockercontainer.Summary{{ID: "k6-1"}}, nil
			}

			service := serviceLabelFromFilters(options.Filters)
			if service == "" {
				return nil, errors.New("missing service label")
			}

			return []dockercontainer.Summary{{ID: service + "-id", State: "exited"}}, nil
		},
	}
	store := &mockRateLimitStore{
		scanResults: []mockScanResult{
			{keys: []string{"rate_limit:1", "rate_limit:2"}, cursor: 0},
		},
	}
	var canceled atomic.Bool

	app := newTestApp(t)
	app.composeProject = "irongate"
	app.docker = docker
	app.redis = store
	app.runner = NewRunner(app.logger, docker, "/tmp/project", "irongate")
	app.active = &scenarioRun{name: "happy-path", cancel: func() { canceled.Store(true) }}
	app.scenarioStatus["happy-path"] = statusRunning
	app.httpClient = newTestHTTPClient(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/health":
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
		case req.URL.Path == "/admin/circuit-breakers/reset":
			return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
		case req.URL.Path == "/chaos/reset":
			return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
		case req.Method == http.MethodGet && req.URL.Path == "/proxies/redis":
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"toxics":[{"name":"latency"}]}`))}, nil
		case req.Method == http.MethodDelete && req.URL.Path == "/proxies/redis/toxics/latency":
			return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
		default:
			t.Fatalf("unexpected reset request: %s %s", req.Method, req.URL.String())
			return nil, nil
		}
	})
	app.toxiproxy = NewToxiproxyClient(app.httpClient, app.logger)

	response := app.resetSystem(context.Background())
	if response.Status != "clean" || !response.ServicesHealthy {
		t.Fatalf("resetSystem response = %#v, want clean healthy reset", response)
	}
	if !canceled.Load() {
		t.Fatal("expected resetSystem to cancel the active scenario")
	}

	docker.mu.Lock()
	defer docker.mu.Unlock()

	if len(docker.stoppedCalls) == 0 || docker.stoppedCalls[0].id != "k6-1" {
		t.Fatalf("expected managed container cleanup, got %#v", docker.stoppedCalls)
	}
	if len(docker.removedCalls) == 0 || docker.removedCalls[0].id != "k6-1" {
		t.Fatalf("expected managed container removal, got %#v", docker.removedCalls)
	}
	if len(docker.startedIDs) != len(serviceEndpoints) {
		t.Fatalf("service start calls = %d, want %d", len(docker.startedIDs), len(serviceEndpoints))
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	if len(store.deleted) != 1 || strings.Join(store.deleted[0], ",") != "rate_limit:1,rate_limit:2" {
		t.Fatalf("deleted redis keys = %#v", store.deleted)
	}
	if len(store.scanCalls) != 1 || store.scanCalls[0].match != "rate_limit:*" {
		t.Fatalf("scan calls = %#v", store.scanCalls)
	}
}

func TestRedisRateLimitStoreNilClientIsSafe(t *testing.T) {
	store := newRedisRateLimitStore(nil)

	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := store.Del(context.Background(), "rate_limit:1"); err != nil {
		t.Fatalf("Del: %v", err)
	}
	keys, cursor, err := store.Scan(context.Background(), 0, "rate_limit:*", 100)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if keys != nil || cursor != 0 {
		t.Fatalf("Scan returned (%#v, %d), want (nil, 0)", keys, cursor)
	}
}

func TestAppCloseCancelsScenarioAndRestoresServices(t *testing.T) {
	docker := &mockDockerClient{
		containerListFunc: func(_ context.Context, options dockercontainer.ListOptions) ([]dockercontainer.Summary, error) {
			if hasManagedContainerLabel(options.Filters) {
				return []dockercontainer.Summary{{ID: "k6-close"}}, nil
			}

			service := serviceLabelFromFilters(options.Filters)
			if service == "" {
				return nil, nil
			}

			return []dockercontainer.Summary{{ID: service + "-id", State: "exited"}}, nil
		},
	}
	store := &mockRateLimitStore{}
	var canceled atomic.Bool

	app := newTestApp(t)
	app.composeProject = "irongate"
	app.docker = docker
	app.redis = store
	app.runner = NewRunner(app.logger, docker, "/tmp/project", "irongate")
	app.active = &scenarioRun{name: "happy-path", cancel: func() { canceled.Store(true) }}
	app.httpClient = newTestHTTPClient(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/health":
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
		case req.URL.Path == "/chaos/reset":
			return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
		default:
			t.Fatalf("unexpected close request: %s %s", req.Method, req.URL.String())
			return nil, nil
		}
	})

	app.close()

	if !canceled.Load() {
		t.Fatal("expected close to cancel the active scenario")
	}

	docker.mu.Lock()
	defer docker.mu.Unlock()

	if docker.closeCalls != 1 {
		t.Fatalf("docker close calls = %d, want 1", docker.closeCalls)
	}
	if len(docker.removedCalls) == 0 || docker.removedCalls[0].id != "k6-close" {
		t.Fatalf("expected close to remove managed k6 container, got %#v", docker.removedCalls)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closeCalls != 1 {
		t.Fatalf("redis close calls = %d, want 1", store.closeCalls)
	}
}
