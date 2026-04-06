package circuitbreaker

import (
	"sync"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

const metricCircuitState = "gateway_circuit_state"

type Registry struct {
	config            config.CBConfig
	breakers          sync.Map
	circuitStateGauge *prometheus.GaugeVec
	statesMu          sync.Mutex
	targetStates      map[string]targetState
}

type targetState struct {
	service string
	state   State
}

func NewRegistry(config config.CBConfig, registerCollector func(prometheus.Collector) error) *Registry {
	registry := &Registry{
		config:            config,
		circuitStateGauge: newCircuitStateGauge(),
		targetStates:      make(map[string]targetState),
	}
	if registerCollector != nil && registry.circuitStateGauge != nil {
		if err := registerCollector(registry.circuitStateGauge); err != nil {
			panic(err)
		}
	}

	return registry
}

func newCircuitStateGauge() *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: metricCircuitState,
			Help: "Current circuit breaker state per service (0=closed, 1=open, 2=half_open).",
		},
		[]string{"service"},
	)
}

func (r *Registry) Breaker(target string) *Breaker {
	return r.BreakerForService(target, target)
}

func (r *Registry) BreakerForService(target, service string) *Breaker {
	if breaker, ok := r.breakers.Load(target); ok {
		return breaker.(*Breaker)
	}

	breaker := New(r.config)
	r.attachBreaker(target, service, breaker)
	actual, loaded := r.breakers.LoadOrStore(target, breaker)
	if loaded {
		return actual.(*Breaker)
	}

	return breaker
}

func (r *Registry) CloneWithConfig(cfg config.CBConfig, registerCollector func(prometheus.Collector) error) *Registry {
	if r == nil {
		return NewRegistry(cfg, registerCollector)
	}

	clone := NewRegistry(cfg, registerCollector)
	r.breakers.Range(func(key, value any) bool {
		target := key.(string)
		service := r.serviceForTarget(target)
		breaker := value.(*Breaker).cloneWithConfig(cfg)
		clone.attachBreaker(target, service, breaker)
		clone.breakers.Store(key, breaker)
		return true
	})

	return clone
}

func (r *Registry) Reset() int {
	if r == nil {
		return 0
	}

	reset := 0
	r.breakers.Range(func(_ any, value any) bool {
		value.(*Breaker).ForceClose()
		reset++
		return true
	})

	return reset
}

func (r *Registry) Collector() prometheus.Collector {
	if r == nil {
		return nil
	}

	return r.circuitStateGauge
}

func (r *Registry) attachBreaker(target, service string, breaker *Breaker) {
	if r == nil || breaker == nil {
		return
	}

	state := breaker.setStateChangeCallback(func(state State) {
		r.setGauge(target, service, state)
	})
	r.setGauge(target, service, state)
}

func (r *Registry) setGauge(target, service string, state State) {
	if r == nil || r.circuitStateGauge == nil {
		return
	}

	service = normalizeService(service)

	r.statesMu.Lock()
	r.targetStates[target] = targetState{
		service: service,
		state:   state,
	}
	aggregate := r.aggregateServiceStateLocked(service)
	r.statesMu.Unlock()

	r.circuitStateGauge.WithLabelValues(service).Set(circuitStateValue(aggregate))
}

func (r *Registry) serviceForTarget(target string) string {
	if r == nil {
		return normalizeService(target)
	}

	r.statesMu.Lock()
	defer r.statesMu.Unlock()

	if state, ok := r.targetStates[target]; ok {
		return state.service
	}

	return normalizeService(target)
}

func (r *Registry) aggregateServiceStateLocked(service string) State {
	sawHalfOpen := false
	for _, state := range r.targetStates {
		if state.service != service {
			continue
		}
		if state.state == StateOpen {
			return StateOpen
		}
		if state.state == StateHalfOpen {
			sawHalfOpen = true
		}
	}
	if sawHalfOpen {
		return StateHalfOpen
	}

	return StateClosed
}

func normalizeService(service string) string {
	if service == "" {
		return "unknown"
	}

	return service
}

func circuitStateValue(state State) float64 {
	switch state {
	case StateOpen:
		return 1
	case StateHalfOpen:
		return 2
	default:
		return 0
	}
}
