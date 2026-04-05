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

func (rr *RoundRobin) Select(options SelectionOptions) (Selection, error) {
	if len(rr.targets) == 0 {
		return Selection{}, ErrNoTargets
	}

	start := rr.next.Add(1) - 1
	for offset := range len(rr.targets) {
		index := (start + uint64(offset)) % uint64(len(rr.targets))
		if isExcluded(options, rr.targets[index]) {
			continue
		}

		return Selection{
			Target: rr.targets[index],
			Done:   noopDone,
		}, nil
	}

	return Selection{}, ErrNoTargets
}
