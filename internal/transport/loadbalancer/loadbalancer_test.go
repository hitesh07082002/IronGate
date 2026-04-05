package loadbalancer

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hitesh07082002/irongate/internal/config"
)

func TestRoundRobinCyclesThroughTargets(t *testing.T) {
	targets := []config.Target{
		{Host: "user-service-1", Port: 8081},
		{Host: "user-service-2", Port: 8091},
		{Host: "user-service-3", Port: 9091},
	}
	balancer := NewRoundRobin(targets)

	got := make([]string, 0, 6)
	for range 6 {
		selection, err := balancer.Select(SelectionOptions{})
		if err != nil {
			t.Fatalf("select target: %v", err)
		}
		got = append(got, selection.Target.Host)
	}

	want := []string{
		"user-service-1",
		"user-service-2",
		"user-service-3",
		"user-service-1",
		"user-service-2",
		"user-service-3",
	}
	assertSequence(t, got, want)
}

func TestRoundRobinSkipsExcludedTargetsWithoutConsumingTurns(t *testing.T) {
	targets := []config.Target{
		{Host: "user-service-1", Port: 8081},
		{Host: "user-service-2", Port: 8091},
		{Host: "user-service-3", Port: 9091},
	}
	balancer := NewRoundRobin(targets)

	got := make([]string, 0, 4)
	for range 4 {
		selection, err := balancer.Select(excludedTargets(targets[1]))
		if err != nil {
			t.Fatalf("select target: %v", err)
		}
		got = append(got, selection.Target.Host)
	}

	assertSequence(t, got, []string{
		"user-service-1",
		"user-service-3",
		"user-service-1",
		"user-service-3",
	})
}

func TestRoundRobinReturnsErrNoTargetsWhenAllTargetsExcluded(t *testing.T) {
	targets := []config.Target{
		{Host: "user-service-1", Port: 8081},
		{Host: "user-service-2", Port: 8091},
	}
	balancer := NewRoundRobin(targets)

	_, err := balancer.Select(excludedTargets(targets...))
	if !errors.Is(err, ErrNoTargets) {
		t.Fatalf("expected ErrNoTargets, got %v", err)
	}
}

func TestWeightedUsesSmoothWeightedRoundRobin(t *testing.T) {
	balancer := NewWeighted([]config.Target{
		{Host: "user-service-1", Port: 8081, Weight: 3},
		{Host: "user-service-2", Port: 8091, Weight: 1},
	})

	got := make([]string, 0, 8)
	for range 8 {
		selection, err := balancer.Select(SelectionOptions{})
		if err != nil {
			t.Fatalf("select target: %v", err)
		}
		got = append(got, selection.Target.Host)
	}

	want := []string{
		"user-service-1",
		"user-service-1",
		"user-service-2",
		"user-service-1",
		"user-service-1",
		"user-service-1",
		"user-service-2",
		"user-service-1",
	}
	assertSequence(t, got, want)
}

func TestWeightedDefaultsMissingWeightsToOne(t *testing.T) {
	balancer := NewWeighted([]config.Target{
		{Host: "order-service-1", Port: 8082},
		{Host: "order-service-2", Port: 8092},
	})

	got := make([]string, 0, 4)
	for range 4 {
		selection, err := balancer.Select(SelectionOptions{})
		if err != nil {
			t.Fatalf("select target: %v", err)
		}
		got = append(got, selection.Target.Host)
	}

	assertSequence(t, got, []string{
		"order-service-1",
		"order-service-2",
		"order-service-1",
		"order-service-2",
	})
}

func TestWeightedSkipsExcludedTargets(t *testing.T) {
	targets := []config.Target{
		{Host: "user-service-1", Port: 8081, Weight: 3},
		{Host: "user-service-2", Port: 8091, Weight: 1},
	}
	balancer := NewWeighted(targets)

	selection, err := balancer.Select(excludedTargets(targets[0]))
	if err != nil {
		t.Fatalf("select target: %v", err)
	}
	if selection.Target.Host != "user-service-2" {
		t.Fatalf("expected excluded selection to skip user-service-1, got %s", selection.Target.Host)
	}
}

func TestWeightedAllExcludedReturnsErrNoTargetsWithoutMutatingState(t *testing.T) {
	targets := []config.Target{
		{Host: "user-service-1", Port: 8081, Weight: 3},
		{Host: "user-service-2", Port: 8091, Weight: 1},
	}
	balancer := NewWeighted(targets)

	_, err := balancer.Select(excludedTargets(targets...))
	if !errors.Is(err, ErrNoTargets) {
		t.Fatalf("expected ErrNoTargets, got %v", err)
	}

	got := make([]string, 0, 4)
	for range 4 {
		selection, err := balancer.Select(SelectionOptions{})
		if err != nil {
			t.Fatalf("select target: %v", err)
		}
		got = append(got, selection.Target.Host)
	}

	assertSequence(t, got, []string{
		"user-service-1",
		"user-service-1",
		"user-service-2",
		"user-service-1",
	})
}

func TestLeastConnPrefersTargetWithFewestActiveRequests(t *testing.T) {
	balancer := NewLeastConn([]config.Target{
		{Host: "order-service-1", Port: 8082},
		{Host: "order-service-2", Port: 8092},
	})

	first, err := balancer.Select(SelectionOptions{})
	if err != nil {
		t.Fatalf("select first target: %v", err)
	}
	if first.Target.Host != "order-service-1" {
		t.Fatalf("expected first target to break tie with order-service-1, got %s", first.Target.Host)
	}

	second, err := balancer.Select(SelectionOptions{})
	if err != nil {
		t.Fatalf("select second target: %v", err)
	}
	if second.Target.Host != "order-service-2" {
		t.Fatalf("expected second target to avoid active connection, got %s", second.Target.Host)
	}

	second.Done()

	third, err := balancer.Select(SelectionOptions{})
	if err != nil {
		t.Fatalf("select third target: %v", err)
	}
	if third.Target.Host != "order-service-2" {
		t.Fatalf("expected third target to keep preferring the least busy instance, got %s", third.Target.Host)
	}

	first.Done()
	third.Done()

	fourth, err := balancer.Select(SelectionOptions{})
	if err != nil {
		t.Fatalf("select fourth target: %v", err)
	}
	defer fourth.Done()

	if fourth.Target.Host != "order-service-1" {
		t.Fatalf("expected balanced pool to return to the first instance on tie, got %s", fourth.Target.Host)
	}
}

func TestLeastConnSkipsExcludedTargetsAndReturnsErrNoTargetsWhenAllExcluded(t *testing.T) {
	targets := []config.Target{
		{Host: "order-service-1", Port: 8082},
		{Host: "order-service-2", Port: 8092},
	}
	balancer := NewLeastConn(targets)

	selection, err := balancer.Select(excludedTargets(targets[0]))
	if err != nil {
		t.Fatalf("select target: %v", err)
	}
	defer selection.Done()

	if selection.Target.Host != "order-service-2" {
		t.Fatalf("expected excluded selection to skip order-service-1, got %s", selection.Target.Host)
	}

	_, err = balancer.Select(excludedTargets(targets...))
	if !errors.Is(err, ErrNoTargets) {
		t.Fatalf("expected ErrNoTargets, got %v", err)
	}
}

func TestLeastConnConcurrentSelectionsReleaseAllActiveCounts(t *testing.T) {
	balancer := NewLeastConn([]config.Target{
		{Host: "payment-service-1", Port: 8083},
		{Host: "payment-service-2", Port: 9083},
	})

	var firstCount atomic.Int64
	var secondCount atomic.Int64

	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			for range 200 {
				selection, err := balancer.Select(SelectionOptions{})
				if err != nil {
					t.Errorf("select target: %v", err)
					return
				}

				if selection.Target.Host == "payment-service-1" {
					firstCount.Add(1)
				} else {
					secondCount.Add(1)
				}

				runtime.Gosched()
				selection.Done()
			}
		}()
	}

	close(start)
	wg.Wait()

	if firstCount.Load() == 0 || secondCount.Load() == 0 {
		t.Fatalf("expected both targets to receive traffic, got first=%d second=%d", firstCount.Load(), secondCount.Load())
	}

	for index, target := range balancer.targets {
		if active := target.active.Load(); active != 0 {
			t.Fatalf("expected target %d to end with zero active requests, got %d", index, active)
		}
	}
}

func excludedTargets(targets ...config.Target) SelectionOptions {
	excluded := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		excluded[targetAddress(target)] = struct{}{}
	}

	return SelectionOptions{ExcludeTargets: excluded}
}

func assertSequence(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("expected %d items, got %d", len(want), len(got))
	}

	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("unexpected sequence at index %d: got %q want %q (full sequence: %v)", index, got[index], want[index], got)
		}
	}
}
