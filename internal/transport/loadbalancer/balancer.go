package loadbalancer

import (
	"errors"
	"fmt"

	"github.com/hitesh07082002/irongate/internal/config"
)

var ErrNoTargets = errors.New("no targets configured")

type Balancer interface {
	Select() (Selection, error)
}

type Selection struct {
	Target config.Target
	Done   func()
}

func New(strategy string, targets []config.Target) (Balancer, error) {
	switch strategy {
	case "round_robin":
		return NewRoundRobin(targets), nil
	case "weighted":
		return NewWeighted(targets), nil
	case "least_conn":
		return NewLeastConn(targets), nil
	default:
		return nil, fmt.Errorf("unknown load balancer strategy %q", strategy)
	}
}

func cloneTargets(targets []config.Target) []config.Target {
	return append([]config.Target(nil), targets...)
}

func noopDone() {}
