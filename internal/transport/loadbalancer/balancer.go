package loadbalancer

import (
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/hitesh07082002/irongate/internal/config"
)

var ErrNoTargets = errors.New("no targets configured")

type Balancer interface {
	Select(options SelectionOptions) (Selection, error)
}

type Selection struct {
	Target config.Target
	Done   func()
}

type SelectionOptions struct {
	ExcludeTargets map[string]struct{}
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

func isExcluded(options SelectionOptions, target config.Target) bool {
	if len(options.ExcludeTargets) == 0 {
		return false
	}

	_, excluded := options.ExcludeTargets[targetAddress(target)]
	return excluded
}

func targetAddress(target config.Target) string {
	return net.JoinHostPort(target.Host, strconv.Itoa(target.Port))
}
