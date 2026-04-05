package circuitbreaker

import (
	"sync"
	"testing"
	"time"

	"github.com/hitesh07082002/irongate/internal/config"
)

func TestBreakerTransitionsClosedOpenHalfOpenClosed(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	breaker := newWithClock(config.CBConfig{
		FailureThreshold:    2,
		SuccessThreshold:    2,
		Timeout:             10 * time.Second,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 2,
	}, clock)

	if !breaker.Allow() {
		t.Fatal("expected closed breaker to allow requests")
	}
	breaker.RecordFailure()
	if breaker.State() != StateClosed {
		t.Fatalf("expected breaker to stay closed after first failure, got %s", breaker.State())
	}

	if !breaker.Allow() {
		t.Fatal("expected closed breaker to keep allowing requests")
	}
	breaker.RecordFailure()
	if breaker.State() != StateOpen {
		t.Fatalf("expected breaker to open after threshold failures, got %s", breaker.State())
	}
	if breaker.Allow() {
		t.Fatal("expected open breaker to reject requests")
	}

	clock.Advance(10 * time.Second)
	if !breaker.Allow() {
		t.Fatal("expected timeout to move breaker into half-open")
	}
	if breaker.State() != StateHalfOpen {
		t.Fatalf("expected half-open state, got %s", breaker.State())
	}
	if !breaker.Allow() {
		t.Fatal("expected second half-open probe to be allowed")
	}
	if breaker.Allow() {
		t.Fatal("expected half-open probe limit to block extra requests")
	}

	breaker.RecordSuccess()
	if breaker.State() != StateHalfOpen {
		t.Fatalf("expected breaker to stay half-open until success threshold, got %s", breaker.State())
	}
	breaker.RecordSuccess()
	if breaker.State() != StateClosed {
		t.Fatalf("expected breaker to close after enough probe successes, got %s", breaker.State())
	}
}

func TestBreakerHalfOpenFailureReopensCircuit(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	breaker := newWithClock(config.CBConfig{
		FailureThreshold:    1,
		SuccessThreshold:    1,
		Timeout:             time.Second,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 1,
	}, clock)

	if !breaker.Allow() {
		t.Fatal("expected closed breaker to allow request")
	}
	breaker.RecordFailure()
	if breaker.State() != StateOpen {
		t.Fatalf("expected open state, got %s", breaker.State())
	}

	clock.Advance(time.Second)
	if !breaker.Allow() {
		t.Fatal("expected half-open probe to be allowed")
	}
	breaker.RecordFailure()
	if breaker.State() != StateOpen {
		t.Fatalf("expected half-open failure to reopen circuit, got %s", breaker.State())
	}
}

func TestRegistryReturnsBreakerPerTarget(t *testing.T) {
	registry := NewRegistry(config.CBConfig{FailureThreshold: 1})

	first := registry.Breaker("user-service-1:8081")
	second := registry.Breaker("user-service-1:8081")
	other := registry.Breaker("user-service-2:8081")

	if first != second {
		t.Fatal("expected same target to reuse breaker instance")
	}
	if first == other {
		t.Fatal("expected different targets to receive different breakers")
	}
}

func TestBreakerConcurrentAccessRaceSafe(t *testing.T) {
	breaker := New(config.CBConfig{
		FailureThreshold:    100000,
		SuccessThreshold:    2,
		Timeout:             time.Second,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 3,
	})

	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			for i := 0; i < 200; i++ {
				if !breaker.Allow() {
					continue
				}

				switch i % 3 {
				case 0:
					breaker.RecordSuccess()
				case 1:
					breaker.RecordFailure()
				default:
					breaker.RecordIgnored()
				}
			}
		}()
	}

	close(start)
	wg.Wait()

	switch breaker.State() {
	case StateClosed, StateOpen, StateHalfOpen:
	default:
		t.Fatalf("unexpected breaker state %q", breaker.State())
	}
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(delay time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delay)
	c.mu.Unlock()
}
