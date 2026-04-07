package circuitbreaker

import (
	"log"
	"sync"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

const metricCircuitState = "gateway_circuit_state"

type Registry struct {
	config            config.CBConfig
	breakers          sync.Map
	breakersMu        sync.Mutex
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
			log.Printf("circuitbreaker: failed to register circuit state gauge: %v", err)
			registry.circuitStateGauge = nil
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
		actual := breaker.(*Breaker)
		r.ensureBreakerService(target, service, actual)
		return actual
	}

	r.breakersMu.Lock()
	defer r.breakersMu.Unlock()

	if breaker, ok := r.breakers.Load(target); ok {
		actual := breaker.(*Breaker)
		r.ensureBreakerService(target, service, actual)
		return actual
	}

	breaker := New(r.config)
	r.attachBreaker(target, service, breaker)
	r.breakers.Store(target, breaker)
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

func (r *Registry) ReconcileTargetServices(targetServices map[string]string) {
	if r == nil {
		return
	}

	r.breakers.Range(func(key, value any) bool {
		target := key.(string)
		breaker := value.(*Breaker)

		service, ok := targetServices[target]
		if !ok {
			breaker.setStateChangeCallback(nil)
			r.breakers.Delete(target)
			r.removeTarget(target)
			return true
		}

		r.ensureBreakerService(target, service, breaker)
		return true
	})
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
	if r == nil || r.circuitStateGauge == nil {
		return nil
	}

	return r.circuitStateGauge
}

func (r *Registry) State(target string) (State, bool) {
	if r == nil {
		return StateClosed, false
	}

	breaker, ok := r.breakers.Load(target)
	if !ok {
		return StateClosed, false
	}

	return breaker.(*Breaker).State(), true
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
	previous, hadPrevious := r.targetStates[target]
	r.targetStates[target] = targetState{
		service: service,
		state:   state,
	}
	aggregate := r.aggregateServiceStateLocked(service)
	oldService := ""
	oldAggregate := StateClosed
	deleteOldSeries := false
	if hadPrevious && previous.service != service {
		oldService = previous.service
		if r.serviceHasTargetsLocked(oldService) {
			oldAggregate = r.aggregateServiceStateLocked(oldService)
		} else {
			deleteOldSeries = true
		}
	}
	if oldService != "" {
		if deleteOldSeries {
			r.circuitStateGauge.DeleteLabelValues(oldService)
		} else {
			r.circuitStateGauge.WithLabelValues(oldService).Set(circuitStateValue(oldAggregate))
		}
	}
	r.circuitStateGauge.WithLabelValues(service).Set(circuitStateValue(aggregate))
	r.statesMu.Unlock()
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

func (r *Registry) ensureBreakerService(target, service string, breaker *Breaker) {
	if r == nil || breaker == nil {
		return
	}

	desired := normalizeService(service)
	current, ok := r.targetState(target)
	if ok && current.service == desired {
		return
	}

	r.attachBreaker(target, desired, breaker)
}

func (r *Registry) targetState(target string) (targetState, bool) {
	if r == nil {
		return targetState{}, false
	}

	r.statesMu.Lock()
	defer r.statesMu.Unlock()

	state, ok := r.targetStates[target]
	return state, ok
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

func (r *Registry) serviceHasTargetsLocked(service string) bool {
	for _, state := range r.targetStates {
		if state.service == service {
			return true
		}
	}

	return false
}

func (r *Registry) removeTarget(target string) {
	if r == nil {
		return
	}

	r.statesMu.Lock()
	defer r.statesMu.Unlock()

	state, ok := r.targetStates[target]
	if !ok {
		return
	}
	delete(r.targetStates, target)

	if r.circuitStateGauge == nil {
		return
	}
	if r.serviceHasTargetsLocked(state.service) {
		r.circuitStateGauge.WithLabelValues(state.service).Set(
			circuitStateValue(r.aggregateServiceStateLocked(state.service)),
		)
		return
	}

	r.circuitStateGauge.DeleteLabelValues(state.service)
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
