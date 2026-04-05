package ratelimit_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hitesh07082002/irongate/internal/ratelimit"
	"github.com/hitesh07082002/irongate/internal/testutil"
)

func TestRedisStoreAllowsUnderLimitRejectsOverLimitAndResets(t *testing.T) {
	client := testutil.RedisClient(t)
	testutil.FlushRedis(t, client)

	store := ratelimit.NewRedisStoreWithClient(client)
	key := ratelimit.Key(fmt.Sprintf("ip:test-%d", time.Now().UnixNano()), "/api/orders")
	ctx := context.Background()

	first, err := store.Allow(ctx, ratelimit.Request{
		Key:      key,
		Limit:    2,
		Window:   150 * time.Millisecond,
		Strategy: "sliding_window",
		Member:   "req-1",
		Now:      time.Now(),
	})
	if err != nil {
		t.Fatalf("first allow: %v", err)
	}
	if !first.Allowed || first.Remaining != 1 {
		t.Fatalf("expected first request allowed with remaining=1, got %+v", first)
	}

	second, err := store.Allow(ctx, ratelimit.Request{
		Key:      key,
		Limit:    2,
		Window:   150 * time.Millisecond,
		Strategy: "sliding_window",
		Member:   "req-2",
		Now:      time.Now(),
	})
	if err != nil {
		t.Fatalf("second allow: %v", err)
	}
	if !second.Allowed || second.Remaining != 0 {
		t.Fatalf("expected second request allowed with remaining=0, got %+v", second)
	}

	third, err := store.Allow(ctx, ratelimit.Request{
		Key:      key,
		Limit:    2,
		Window:   150 * time.Millisecond,
		Strategy: "sliding_window",
		Member:   "req-3",
		Now:      time.Now(),
	})
	if err != nil {
		t.Fatalf("third allow: %v", err)
	}
	if third.Allowed || third.Remaining != 0 {
		t.Fatalf("expected third request rejected with remaining=0, got %+v", third)
	}
	if !third.ResetAt.After(time.Now()) {
		t.Fatalf("expected reset time in the future, got %s", third.ResetAt)
	}

	time.Sleep(175 * time.Millisecond)

	afterReset, err := store.Allow(ctx, ratelimit.Request{
		Key:      key,
		Limit:    2,
		Window:   150 * time.Millisecond,
		Strategy: "sliding_window",
		Member:   "req-4",
		Now:      time.Now(),
	})
	if err != nil {
		t.Fatalf("allow after reset: %v", err)
	}
	if !afterReset.Allowed || afterReset.Remaining != 1 {
		t.Fatalf("expected request after reset allowed with remaining=1, got %+v", afterReset)
	}
}

func TestRedisStoreConcurrentBoundaryIsAtomic(t *testing.T) {
	client := testutil.RedisClient(t)
	testutil.FlushRedis(t, client)

	store := ratelimit.NewRedisStoreWithClient(client)
	key := ratelimit.Key(fmt.Sprintf("ip:atomic-%d", time.Now().UnixNano()), "/api/orders")
	ctx := context.Background()

	const (
		limit       = 25
		concurrency = 50
	)

	start := make(chan struct{})
	var allowed atomic.Int64
	var rejected atomic.Int64
	errCh := make(chan error, concurrency)
	var wg sync.WaitGroup

	for index := 0; index < concurrency; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start

			decision, err := store.Allow(ctx, ratelimit.Request{
				Key:      key,
				Limit:    limit,
				Window:   time.Second,
				Strategy: "sliding_window",
				Member:   fmt.Sprintf("req-%d", index),
				Now:      time.Now(),
			})
			if err != nil {
				errCh <- err
				return
			}
			if decision.Allowed {
				allowed.Add(1)
				return
			}

			rejected.Add(1)
		}(index)
	}

	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent allow returned error: %v", err)
		}
	}

	if got := allowed.Load(); got != limit {
		t.Fatalf("expected %d allowed requests, got %d", limit, got)
	}
	if got := rejected.Load(); got != concurrency-limit {
		t.Fatalf("expected %d rejected requests, got %d", concurrency-limit, got)
	}
}
