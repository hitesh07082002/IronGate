package circuitbreaker

import (
	"sync"
	"time"

	"github.com/hitesh07082002/irongate/internal/config"
)

type State string

const (
	StateClosed   State = "closed"
	StateOpen     State = "open"
	StateHalfOpen State = "half-open"
)

type timeSource interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

type Breaker struct {
	mu sync.Mutex

	config config.CBConfig
	clock  timeSource

	stateChangeCallback func(State)

	state             State
	openUntil         time.Time
	failures          []time.Time
	halfOpenSuccesses int
	halfOpenInFlight  int
}

func New(config config.CBConfig) *Breaker {
	return newWithClock(config, realClock{})
}

func newWithClock(config config.CBConfig, clock timeSource) *Breaker {
	return &Breaker{
		config: normalizeConfig(config),
		clock:  clock,
		state:  StateClosed,
	}
}

func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.clock.Now()
	if b.state == StateOpen && !now.Before(b.openUntil) {
		b.state = StateHalfOpen
		b.halfOpenSuccesses = 0
		b.halfOpenInFlight = 0
		if b.stateChangeCallback != nil {
			b.stateChangeCallback(StateHalfOpen)
		}
	}

	switch b.state {
	case StateClosed:
		return true
	case StateOpen:
		return false
	case StateHalfOpen:
		if b.halfOpenInFlight >= b.config.HalfOpenMaxRequests {
			return false
		}
		b.halfOpenInFlight++
		return true
	default:
		return false
	}
}

func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		b.pruneFailuresLocked(b.clock.Now())
	case StateHalfOpen:
		if b.halfOpenInFlight > 0 {
			b.halfOpenInFlight--
		}
		b.halfOpenSuccesses++
		if b.halfOpenSuccesses >= b.config.SuccessThreshold && b.halfOpenInFlight == 0 {
			b.closeLocked()
		}
	}
}

func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.clock.Now()
	switch b.state {
	case StateClosed:
		b.failures = append(b.failures, now)
		b.pruneFailuresLocked(now)
		if len(b.failures) >= b.config.FailureThreshold {
			b.openLocked(now)
		}
	case StateHalfOpen:
		if b.halfOpenInFlight > 0 {
			b.halfOpenInFlight--
		}
		b.openLocked(now)
	}
}

func (b *Breaker) RecordIgnored() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == StateHalfOpen && b.halfOpenInFlight > 0 {
		b.halfOpenInFlight--
		if b.halfOpenSuccesses >= b.config.SuccessThreshold && b.halfOpenInFlight == 0 {
			b.closeLocked()
		}
	}
}

func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.clock.Now()
	if b.state == StateOpen && !now.Before(b.openUntil) {
		return StateHalfOpen
	}

	return b.state
}

func (b *Breaker) cloneWithConfig(cfg config.CBConfig) *Breaker {
	if b == nil {
		return New(cfg)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	clone := &Breaker{
		config:              normalizeConfig(cfg),
		clock:               b.clock,
		stateChangeCallback: b.stateChangeCallback,
		state:               b.state,
		openUntil:           b.openUntil,
		failures:            append([]time.Time(nil), b.failures...),
		halfOpenSuccesses:   b.halfOpenSuccesses,
		halfOpenInFlight:    b.halfOpenInFlight,
	}
	clone.reconcileLocked(b.config)
	return clone
}

func (b *Breaker) openLocked(now time.Time) {
	b.state = StateOpen
	b.openUntil = now.Add(b.config.Timeout)
	b.failures = nil
	b.halfOpenSuccesses = 0
	b.halfOpenInFlight = 0
	if b.stateChangeCallback != nil {
		b.stateChangeCallback(StateOpen)
	}
}

func (b *Breaker) closeLocked() {
	b.state = StateClosed
	b.openUntil = time.Time{}
	b.failures = nil
	b.halfOpenSuccesses = 0
	b.halfOpenInFlight = 0
	if b.stateChangeCallback != nil {
		b.stateChangeCallback(StateClosed)
	}
}

func (b *Breaker) ForceClose() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.state = StateClosed
	b.openUntil = time.Time{}
	b.failures = nil
	b.halfOpenSuccesses = 0
	b.halfOpenInFlight = 0
	if b.stateChangeCallback != nil {
		b.stateChangeCallback(StateClosed)
	}
}

func (b *Breaker) pruneFailuresLocked(now time.Time) {
	if b.config.WindowSize <= 0 || len(b.failures) == 0 {
		return
	}

	cutoff := now.Add(-b.config.WindowSize)
	trimmed := b.failures[:0]
	for _, failureAt := range b.failures {
		if failureAt.Before(cutoff) {
			continue
		}
		trimmed = append(trimmed, failureAt)
	}
	b.failures = trimmed
}

func (b *Breaker) reconcileLocked(previousConfig config.CBConfig) {
	now := b.clock.Now()
	b.pruneFailuresLocked(now)

	switch b.state {
	case StateClosed:
		if len(b.failures) >= b.config.FailureThreshold {
			b.openLocked(now)
		}
	case StateOpen:
		previous := normalizeConfig(previousConfig)
		if !b.openUntil.IsZero() {
			openedAt := b.openUntil.Add(-previous.Timeout)
			b.openUntil = openedAt.Add(b.config.Timeout)
		}
	case StateHalfOpen:
		if b.halfOpenInFlight > b.config.HalfOpenMaxRequests {
			b.halfOpenInFlight = b.config.HalfOpenMaxRequests
		}
		if b.halfOpenSuccesses >= b.config.SuccessThreshold && b.halfOpenInFlight == 0 {
			b.closeLocked()
		}
	}
}

func normalizeConfig(cfg config.CBConfig) config.CBConfig {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.SuccessThreshold <= 0 {
		cfg.SuccessThreshold = 3
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.WindowSize <= 0 {
		cfg.WindowSize = 60 * time.Second
	}
	if cfg.HalfOpenMaxRequests <= 0 {
		cfg.HalfOpenMaxRequests = 3
	}
	return cfg
}

func (b *Breaker) setStateChangeCallback(callback func(State)) State {
	if b == nil {
		return StateClosed
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.stateChangeCallback = callback
	return b.state
}
