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

func (lc *LeastConn) Select(options SelectionOptions) (Selection, error) {
	if len(lc.targets) == 0 {
		return Selection{}, ErrNoTargets
	}

	var best *leastConnTarget
	var bestActive int64
	for _, candidate := range lc.targets {
		if isExcluded(options, candidate.target) {
			continue
		}

		candidateActive := candidate.active.Load()
		if best == nil || candidateActive < bestActive {
			best = candidate
			bestActive = candidateActive
		}
	}

	if best == nil {
		return Selection{}, ErrNoTargets
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
