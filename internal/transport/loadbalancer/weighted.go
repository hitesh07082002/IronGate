package loadbalancer

import (
	"sync"

	"github.com/hitesh07082002/irongate/internal/config"
)

type Weighted struct {
	mu      sync.Mutex
	targets []weightedTarget
}

type weightedTarget struct {
	target        config.Target
	weight        int
	currentWeight int
}

func NewWeighted(targets []config.Target) *Weighted {
	weightedTargets := make([]weightedTarget, 0, len(targets))
	for _, target := range targets {
		weightedTargets = append(weightedTargets, weightedTarget{
			target: target,
			weight: effectiveWeight(target),
		})
	}

	return &Weighted{targets: weightedTargets}
}

func (w *Weighted) Select(options SelectionOptions) (Selection, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.targets) == 0 {
		return Selection{}, ErrNoTargets
	}

	bestIndex := -1
	totalWeight := 0
	for index := range w.targets {
		if isExcluded(options, w.targets[index].target) {
			continue
		}

		w.targets[index].currentWeight += w.targets[index].weight
		totalWeight += w.targets[index].weight

		if bestIndex == -1 || w.targets[index].currentWeight > w.targets[bestIndex].currentWeight {
			bestIndex = index
		}
	}

	if bestIndex == -1 {
		return Selection{}, ErrNoTargets
	}

	w.targets[bestIndex].currentWeight -= totalWeight
	return Selection{
		Target: w.targets[bestIndex].target,
		Done:   noopDone,
	}, nil
}

func effectiveWeight(target config.Target) int {
	if target.Weight <= 0 {
		return 1
	}
	return target.Weight
}
