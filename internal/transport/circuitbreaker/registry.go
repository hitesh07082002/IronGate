package circuitbreaker

import (
	"sync"

	"github.com/hitesh07082002/irongate/internal/config"
)

type Registry struct {
	config   config.CBConfig
	breakers sync.Map
}

func NewRegistry(config config.CBConfig) *Registry {
	return &Registry{config: config}
}

func (r *Registry) Breaker(target string) *Breaker {
	if breaker, ok := r.breakers.Load(target); ok {
		return breaker.(*Breaker)
	}

	breaker := New(r.config)
	actual, _ := r.breakers.LoadOrStore(target, breaker)
	return actual.(*Breaker)
}
