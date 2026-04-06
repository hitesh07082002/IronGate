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
}

func NewRegistry(config config.CBConfig, registerCollector func(prometheus.Collector) error) *Registry {
	registry := &Registry{
		config: config,
		circuitStateGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: metricCircuitState,
				Help: "Current circuit breaker state per target (0=closed, 1=open, 2=half_open).",
			},
			[]string{"target"},
		),
	}
	if registerCollector != nil && registry.circuitStateGauge != nil {
		if err := registerCollector(registry.circuitStateGauge); err != nil {
			panic(err)
		}
	}

	return registry
}

func (r *Registry) Breaker(target string) *Breaker {
	if breaker, ok := r.breakers.Load(target); ok {
		return breaker.(*Breaker)
	}

	breaker := New(r.config)
	r.attachBreaker(target, breaker)
	actual, loaded := r.breakers.LoadOrStore(target, breaker)
	if loaded {
		return actual.(*Breaker)
	}

	return breaker
}

func (r *Registry) CloneWithConfig(cfg config.CBConfig) *Registry {
	if r == nil {
		return NewRegistry(cfg, nil)
	}

	clone := &Registry{
		config:            cfg,
		circuitStateGauge: r.circuitStateGauge,
	}
	r.breakers.Range(func(key, value any) bool {
		target := key.(string)
		breaker := value.(*Breaker).cloneWithConfig(cfg)
		clone.attachBreaker(target, breaker)
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

func (r *Registry) attachBreaker(target string, breaker *Breaker) {
	if r == nil || breaker == nil {
		return
	}

	state := breaker.setStateChangeCallback(func(state State) {
		r.setGauge(target, state)
	})
	r.setGauge(target, state)
}

func (r *Registry) setGauge(target string, state State) {
	if r == nil || r.circuitStateGauge == nil {
		return
	}

	r.circuitStateGauge.WithLabelValues(target).Set(circuitStateValue(state))
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
