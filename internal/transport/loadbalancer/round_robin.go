package loadbalancer

import (
	"sync/atomic"

	"github.com/hitesh07082002/irongate/internal/config"
)

type RoundRobin struct {
	targets []config.Target
	next    atomic.Uint64
}

func NewRoundRobin(targets []config.Target) *RoundRobin {
	return &RoundRobin{targets: cloneTargets(targets)}
}

func (rr *RoundRobin) Select() (Selection, error) {
	if len(rr.targets) == 0 {
		return Selection{}, ErrNoTargets
	}

	index := (rr.next.Add(1) - 1) % uint64(len(rr.targets))
	return Selection{
		Target: rr.targets[index],
		Done:   noopDone,
	}, nil
}
