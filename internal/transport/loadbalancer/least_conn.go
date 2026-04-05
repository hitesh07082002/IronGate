package loadbalancer

import (
	"sync"
	"sync/atomic"

	"github.com/hitesh07082002/irongate/internal/config"
)

type LeastConn struct {
	targets []*leastConnTarget
}

type leastConnTarget struct {
	target config.Target
	active atomic.Int64
}

func NewLeastConn(targets []config.Target) *LeastConn {
	leastConnTargets := make([]*leastConnTarget, 0, len(targets))
	for _, target := range targets {
		leastConnTargets = append(leastConnTargets, &leastConnTarget{target: target})
	}

	return &LeastConn{targets: leastConnTargets}
}

func (lc *LeastConn) Select() (Selection, error) {
	if len(lc.targets) == 0 {
		return Selection{}, ErrNoTargets
	}

	best := lc.targets[0]
	bestActive := best.active.Load()
	for _, candidate := range lc.targets[1:] {
		candidateActive := candidate.active.Load()
		if candidateActive < bestActive {
			best = candidate
			bestActive = candidateActive
		}
	}

	best.active.Add(1)

	var once sync.Once
	return Selection{
		Target: best.target,
		Done: func() {
			once.Do(func() {
				best.active.Add(-1)
			})
		},
	}, nil
}
