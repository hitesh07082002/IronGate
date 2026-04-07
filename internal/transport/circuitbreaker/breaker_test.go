package circuitbreaker

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/hitesh07082002/irongate/internal/metrics"
	dto "github.com/prometheus/client_model/go"
)

func TestBreakerTransitionsClosedOpenHalfOpenClosed(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	breaker := newWithClock(config.CBConfig{
		FailureThreshold:    2,
		SuccessThreshold:    2,
		Timeout:             10 * time.Second,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 2,
	}, clock)

	if !breaker.Allow() {
		t.Fatal("expected closed breaker to allow requests")
	}
	breaker.RecordFailure()
	if breaker.State() != StateClosed {
		t.Fatalf("expected breaker to stay closed after first failure, got %s", breaker.State())
	}

	if !breaker.Allow() {
		t.Fatal("expected closed breaker to keep allowing requests")
	}
	breaker.RecordFailure()
	if breaker.State() != StateOpen {
		t.Fatalf("expected breaker to open after threshold failures, got %s", breaker.State())
	}
	if breaker.Allow() {
		t.Fatal("expected open breaker to reject requests")
	}

	clock.Advance(10 * time.Second)
	if !breaker.Allow() {
		t.Fatal("expected timeout to move breaker into half-open")
	}
	if breaker.State() != StateHalfOpen {
		t.Fatalf("expected half-open state, got %s", breaker.State())
	}
	if !breaker.Allow() {
		t.Fatal("expected second half-open probe to be allowed")
	}
	if breaker.Allow() {
		t.Fatal("expected half-open probe limit to block extra requests")
	}

	breaker.RecordSuccess()
	if breaker.State() != StateHalfOpen {
		t.Fatalf("expected breaker to stay half-open until success threshold, got %s", breaker.State())
	}
	breaker.RecordSuccess()
	if breaker.State() != StateClosed {
		t.Fatalf("expected breaker to close after enough probe successes, got %s", breaker.State())
	}
}

func TestBreakerHalfOpenFailureReopensCircuit(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	breaker := newWithClock(config.CBConfig{
		FailureThreshold:    1,
		SuccessThreshold:    1,
		Timeout:             time.Second,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 1,
	}, clock)

	if !breaker.Allow() {
		t.Fatal("expected closed breaker to allow request")
	}
	breaker.RecordFailure()
	if breaker.State() != StateOpen {
		t.Fatalf("expected open state, got %s", breaker.State())
	}

	clock.Advance(time.Second)
	if !breaker.Allow() {
		t.Fatal("expected half-open probe to be allowed")
	}
	breaker.RecordFailure()
	if breaker.State() != StateOpen {
		t.Fatalf("expected half-open failure to reopen circuit, got %s", breaker.State())
	}
}

func TestBreakerHalfOpenWaitsForAllProbeCompletionsBeforeClosing(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	breaker := newWithClock(config.CBConfig{
		FailureThreshold:    1,
		SuccessThreshold:    1,
		Timeout:             time.Second,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 2,
	}, clock)

	if !breaker.Allow() {
		t.Fatal("expected closed breaker to allow request")
	}
	breaker.RecordFailure()

	clock.Advance(time.Second)
	if !breaker.Allow() {
		t.Fatal("expected first half-open probe to be allowed")
	}
	if !breaker.Allow() {
		t.Fatal("expected second half-open probe to be allowed")
	}

	breaker.RecordSuccess()
	if breaker.State() != StateHalfOpen {
		t.Fatalf("expected breaker to remain half-open while another probe is in flight, got %s", breaker.State())
	}

	breaker.RecordFailure()
	if breaker.State() != StateOpen {
		t.Fatalf("expected late half-open failure to reopen circuit, got %s", breaker.State())
	}
}

func TestBreakerHalfOpenIgnoredProbeCanFinishClosing(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	breaker := newWithClock(config.CBConfig{
		FailureThreshold:    1,
		SuccessThreshold:    1,
		Timeout:             time.Second,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 2,
	}, clock)

	if !breaker.Allow() {
		t.Fatal("expected closed breaker to allow request")
	}
	breaker.RecordFailure()

	clock.Advance(time.Second)
	if !breaker.Allow() {
		t.Fatal("expected first half-open probe to be allowed")
	}
	if !breaker.Allow() {
		t.Fatal("expected second half-open probe to be allowed")
	}

	breaker.RecordSuccess()
	breaker.RecordIgnored()
	if breaker.State() != StateClosed {
		t.Fatalf("expected breaker to close after the final in-flight probe completes, got %s", breaker.State())
	}
}

func TestRegistryReturnsBreakerPerTarget(t *testing.T) {
	registry := NewRegistry(config.CBConfig{FailureThreshold: 1}, nil)

	first := registry.Breaker("user-service-1:8081")
	second := registry.Breaker("user-service-1:8081")
	other := registry.Breaker("user-service-2:8081")

	if first != second {
		t.Fatal("expected same target to reuse breaker instance")
	}
	if first == other {
		t.Fatal("expected different targets to receive different breakers")
	}
}

func TestRegistryBreakerForServiceConcurrentSameTargetReusesStoredBreaker(t *testing.T) {
	metricsRegistry := metrics.NewRegistry()
	registry := NewRegistry(config.CBConfig{FailureThreshold: 1}, metricsRegistry.RegisterCollector)

	const workers = 100
	start := make(chan struct{})
	results := make(chan *Breaker, workers)
	var wg sync.WaitGroup

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- registry.BreakerForService("user-service-1:8081", "user-service")
		}()
	}

	close(start)
	wg.Wait()
	close(results)

	var first *Breaker
	for breaker := range results {
		if first == nil {
			first = breaker
			continue
		}
		if breaker != first {
			t.Fatal("expected concurrent lookup to reuse the stored breaker instance")
		}
	}

	state, ok := registry.targetState("user-service-1:8081")
	if !ok {
		t.Fatal("expected concurrent lookup to record target state")
	}
	if state.service != "user-service" {
		t.Fatalf("expected target to remain mapped to user-service, got %q", state.service)
	}
	if got := circuitStateGaugeValueForService(t, metricsRegistry, "user-service"); got != 0 {
		t.Fatalf("expected concurrent lookup to keep gauge closed, got %v", got)
	}
}

func TestRegistryCloneWithConfigPreservesOpenState(t *testing.T) {
	registry := NewRegistry(config.CBConfig{
		FailureThreshold:    1,
		SuccessThreshold:    1,
		Timeout:             time.Minute,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 1,
	}, nil)

	breaker := registry.Breaker("user-service-1:8081")
	if !breaker.Allow() {
		t.Fatal("expected closed breaker to allow request")
	}
	breaker.RecordFailure()
	if breaker.State() != StateOpen {
		t.Fatalf("expected breaker to open, got %s", breaker.State())
	}

	cloned := registry.CloneWithConfig(config.CBConfig{
		FailureThreshold:    2,
		SuccessThreshold:    1,
		Timeout:             time.Minute,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 1,
	}, nil)
	if cloned == nil {
		t.Fatal("expected cloned registry")
	}

	clonedBreaker := cloned.Breaker("user-service-1:8081")
	if clonedBreaker.State() != StateOpen {
		t.Fatalf("expected cloned breaker to stay open, got %s", clonedBreaker.State())
	}
	if clonedBreaker.Allow() {
		t.Fatal("expected cloned open breaker to reject requests")
	}
}

func TestBreakerConcurrentAccessRaceSafe(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	breaker := newWithClock(config.CBConfig{
		FailureThreshold:    1,
		SuccessThreshold:    1,
		Timeout:             time.Second,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 2,
	}, clock)

	runConcurrentWorkers(100, func() {
		if !breaker.Allow() {
			return
		}
		breaker.RecordFailure()
	})

	if breaker.State() != StateOpen {
		t.Fatalf("expected breaker to open under concurrent failures, got %s", breaker.State())
	}

	clock.Advance(time.Second)

	var halfOpenCompletions atomic.Int32
	runConcurrentWorkers(100, func() {
		if !breaker.Allow() {
			return
		}

		if halfOpenCompletions.Add(1) == 1 {
			breaker.RecordSuccess()
			return
		}

		breaker.RecordIgnored()
	})

	if breaker.State() != StateClosed {
		t.Fatalf("expected breaker to close after concurrent half-open probes, got %s", breaker.State())
	}
}

func TestReset_AllBreakers(t *testing.T) {
	registry := NewRegistry(config.CBConfig{
		FailureThreshold:    1,
		SuccessThreshold:    1,
		Timeout:             time.Minute,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 1,
	}, nil)

	first := registry.Breaker("user-service-1:8081")
	second := registry.Breaker("user-service-2:8081")

	if !first.Allow() || !second.Allow() {
		t.Fatal("expected closed breakers to allow requests")
	}
	first.RecordFailure()
	second.RecordFailure()

	if got := registry.Reset(); got != 2 {
		t.Fatalf("expected reset to clear 2 targets, got %d", got)
	}
	if first.State() != StateClosed {
		t.Fatalf("expected first breaker closed after reset, got %s", first.State())
	}
	if second.State() != StateClosed {
		t.Fatalf("expected second breaker closed after reset, got %s", second.State())
	}
}

func TestReset_Race(t *testing.T) {
	registry := NewRegistry(config.CBConfig{
		FailureThreshold:    1,
		SuccessThreshold:    1,
		Timeout:             time.Millisecond,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 1,
	}, nil)
	breakers := []*Breaker{
		registry.Breaker("user-service-1:8081"),
		registry.Breaker("user-service-2:8081"),
		registry.Breaker("user-service-3:8081"),
	}

	var sequence atomic.Uint32
	runConcurrentWorkers(100, func() {
		index := int(sequence.Add(1))
		breaker := breakers[index%len(breakers)]

		switch index % 3 {
		case 0:
			if breaker.Allow() {
				breaker.RecordFailure()
			}
		case 1:
			_ = breaker.State()
		default:
			registry.Reset()
		}
	})

	if got := registry.Reset(); got != len(breakers) {
		t.Fatalf("expected final reset to clear %d targets, got %d", len(breakers), got)
	}
	for index, breaker := range breakers {
		if breaker.State() != StateClosed {
			t.Fatalf("expected breaker %d closed after race reset, got %s", index, breaker.State())
		}
	}
}

func TestCircuitStateGauge_Transitions(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	cfg := config.CBConfig{
		FailureThreshold:    1,
		SuccessThreshold:    1,
		Timeout:             time.Second,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 1,
	}
	metricsRegistry := metrics.NewRegistry()
	registry := NewRegistry(cfg, metricsRegistry.RegisterCollector)
	breaker := newWithClock(cfg, clock)
	registry.breakers.Store("user-service-1:8081", breaker)
	registry.attachBreaker("user-service-1:8081", "user-service", breaker)

	if got := circuitStateGaugeValueForService(t, metricsRegistry, "user-service"); got != 0 {
		t.Fatalf("expected initial closed gauge value 0, got %v", got)
	}

	if !breaker.Allow() {
		t.Fatal("expected closed breaker to allow request")
	}
	breaker.RecordFailure()
	if got := circuitStateGaugeValueForService(t, metricsRegistry, "user-service"); got != 1 {
		t.Fatalf("expected open gauge value 1, got %v", got)
	}

	clock.Advance(time.Second)
	if !breaker.Allow() {
		t.Fatal("expected half-open probe to be allowed")
	}
	if got := circuitStateGaugeValueForService(t, metricsRegistry, "user-service"); got != 2 {
		t.Fatalf("expected half-open gauge value 2, got %v", got)
	}

	breaker.RecordSuccess()
	if got := circuitStateGaugeValueForService(t, metricsRegistry, "user-service"); got != 0 {
		t.Fatalf("expected closed gauge value 0 after recovery, got %v", got)
	}
}

func TestCircuitStateGauge_AggregatesByService(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	cfg := config.CBConfig{
		FailureThreshold:    1,
		SuccessThreshold:    1,
		Timeout:             time.Second,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 1,
	}
	metricsRegistry := metrics.NewRegistry()
	registry := NewRegistry(cfg, metricsRegistry.RegisterCollector)

	openBreaker := newWithClock(cfg, clock)
	registry.breakers.Store("user-service-1:8081", openBreaker)
	registry.attachBreaker("user-service-1:8081", "user-service", openBreaker)
	openBreaker.RecordFailure()

	halfOpenBreaker := newWithClock(cfg, clock)
	registry.breakers.Store("user-service-2:8081", halfOpenBreaker)
	registry.attachBreaker("user-service-2:8081", "user-service", halfOpenBreaker)
	halfOpenBreaker.RecordFailure()
	clock.Advance(time.Second)
	if !halfOpenBreaker.Allow() {
		t.Fatal("expected second breaker to enter half-open")
	}

	if got := circuitStateGaugeValueForService(t, metricsRegistry, "user-service"); got != 1 {
		t.Fatalf("expected open to dominate aggregate state, got %v", got)
	}

	openBreaker.ForceClose()
	if got := circuitStateGaugeValueForService(t, metricsRegistry, "user-service"); got != 2 {
		t.Fatalf("expected half-open aggregate after open breaker recovered, got %v", got)
	}
}

func TestRegistryCloneWithConfigUsesFreshCollector(t *testing.T) {
	registry := NewRegistry(config.CBConfig{FailureThreshold: 1}, nil)
	breaker := registry.BreakerForService("user-service-1:8081", "user-service")
	if !breaker.Allow() {
		t.Fatal("expected breaker to allow request before opening")
	}
	breaker.RecordFailure()

	cloned := registry.CloneWithConfig(config.CBConfig{FailureThreshold: 1}, nil)
	if cloned == nil {
		t.Fatal("expected cloned registry")
	}
	if cloned.Collector() == registry.Collector() {
		t.Fatal("expected cloned registry to use a fresh collector")
	}
	if cloned.BreakerForService("user-service-1:8081", "user-service").State() != StateOpen {
		t.Fatal("expected cloned breaker to preserve open state")
	}
}

func TestRegistryBreakerForServiceReplacesRawTargetSeries(t *testing.T) {
	metricsRegistry := metrics.NewRegistry()
	registry := NewRegistry(config.CBConfig{FailureThreshold: 1}, metricsRegistry.RegisterCollector)

	breaker := registry.Breaker("user-service-1:8081")
	if !breaker.Allow() {
		t.Fatal("expected initial breaker to allow request")
	}
	breaker.RecordFailure()

	if !circuitStateGaugeHasService(t, metricsRegistry, "user-service-1:8081") {
		t.Fatal("expected raw target series before service mapping is known")
	}

	serviceBreaker := registry.BreakerForService("user-service-1:8081", "user-service")
	if serviceBreaker != breaker {
		t.Fatal("expected service lookup to reuse the cached breaker")
	}
	if got := circuitStateGaugeValueForService(t, metricsRegistry, "user-service"); got != 1 {
		t.Fatalf("expected remapped service series to reflect the open breaker, got %v", got)
	}
	if circuitStateGaugeHasService(t, metricsRegistry, "user-service-1:8081") {
		t.Fatal("expected raw target series to be removed after remapping to the service label")
	}
}

func TestRegistryStateDoesNotCreateBreakers(t *testing.T) {
	metricsRegistry := metrics.NewRegistry()
	registry := NewRegistry(config.CBConfig{FailureThreshold: 1}, metricsRegistry.RegisterCollector)

	if state, ok := registry.State("user-service-1:8081"); ok || state != StateClosed {
		t.Fatalf("expected missing breaker state lookup to return closed,false, got %s,%t", state, ok)
	}
	if circuitStateGaugeHasService(t, metricsRegistry, "user-service-1:8081") {
		t.Fatal("expected state lookups to avoid creating a raw target gauge series")
	}
}

func TestRegistryReconcileTargetServicesRemovesRetiredTargets(t *testing.T) {
	metricsRegistry := metrics.NewRegistry()
	registry := NewRegistry(config.CBConfig{FailureThreshold: 1}, metricsRegistry.RegisterCollector)

	kept := registry.BreakerForService("user-service-1:8081", "user-service")
	retired := registry.BreakerForService("order-service-1:8082", "order-service")
	kept.RecordFailure()
	retired.RecordFailure()

	registry.ReconcileTargetServices(map[string]string{
		"user-service-1:8081": "renamed-user-service",
	})

	if _, ok := registry.State("order-service-1:8082"); ok {
		t.Fatal("expected retired target to be removed from the registry")
	}
	if got := registry.Reset(); got != 1 {
		t.Fatalf("expected reset to clear only the kept breaker, got %d", got)
	}
	if circuitStateGaugeHasService(t, metricsRegistry, "order-service") {
		t.Fatal("expected retired service gauge series to be removed")
	}
	if circuitStateGaugeHasService(t, metricsRegistry, "user-service") {
		t.Fatal("expected previous service label to be removed after rename")
	}
	if got := circuitStateGaugeValueForService(t, metricsRegistry, "renamed-user-service"); got != 0 {
		t.Fatalf("expected renamed service gauge to reset to 0, got %v", got)
	}
}

func TestCircuitStateGaugeConcurrentTransitionsRemainConsistent(t *testing.T) {
	cfg := config.CBConfig{
		FailureThreshold:    1,
		SuccessThreshold:    1,
		Timeout:             time.Minute,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 1,
	}
	metricsRegistry := metrics.NewRegistry()
	registry := NewRegistry(cfg, metricsRegistry.RegisterCollector)
	first := registry.BreakerForService("user-service-1:8081", "user-service")
	second := registry.BreakerForService("user-service-2:8081", "user-service")

	var sequence atomic.Uint32
	runConcurrentWorkers(100, func() {
		switch sequence.Add(1) % 4 {
		case 0:
			first.ForceClose()
		case 1:
			second.ForceClose()
		case 2:
			first.RecordFailure()
		default:
			second.RecordFailure()
		}
	})

	registry.statesMu.Lock()
	want := circuitStateValue(registry.aggregateServiceStateLocked("user-service"))
	registry.statesMu.Unlock()
	if got := circuitStateGaugeValueForService(t, metricsRegistry, "user-service"); got != want {
		t.Fatalf("expected concurrent gauge updates to end at %v, got %v", want, got)
	}
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(delay time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delay)
	c.mu.Unlock()
}

func runConcurrentWorkers(workers int, fn func()) {
	start := make(chan struct{})
	var wg sync.WaitGroup

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			fn()
		}()
	}

	close(start)
	wg.Wait()
}

func circuitStateGaugeValueForService(t *testing.T, registry *metrics.Registry, service string) float64 {
	t.Helper()

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	for _, family := range families {
		if family.GetName() != metricCircuitState {
			continue
		}
		for _, metric := range family.Metric {
			if labelValue(metric, "service") == service {
				if metric.Gauge == nil {
					t.Fatalf("metric %s for service %s is not a gauge", metricCircuitState, service)
				}
				return metric.GetGauge().GetValue()
			}
		}
	}

	t.Fatalf("metric %s for service %s not found", metricCircuitState, service)
	return 0
}

func circuitStateGaugeHasService(t *testing.T, registry *metrics.Registry, service string) bool {
	t.Helper()

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	for _, family := range families {
		if family.GetName() != metricCircuitState {
			continue
		}
		for _, metric := range family.Metric {
			if labelValue(metric, "service") == service {
				return true
			}
		}
	}

	return false
}

func labelValue(metric *dto.Metric, labelName string) string {
	for _, label := range metric.Label {
		if label.GetName() == labelName {
			return label.GetValue()
		}
	}

	return ""
}
