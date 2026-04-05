package ratelimit_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/hitesh07082002/irongate/internal/ratelimit"
	"github.com/hitesh07082002/irongate/internal/testutil"
)

func TestRedisStoreAllowsUnderLimitRejectsOverLimitAndResets(t *testing.T) {
	client := testutil.RedisClient(t)

	store := ratelimit.NewRedisStoreWithClient(client)
	key := ratelimit.Key(fmt.Sprintf("ip:test-%d", time.Now().UnixNano()), "/api/orders")
	ctx := context.Background()
	base := time.Now()

	first, err := store.Allow(ctx, ratelimit.Request{
		Key:      key,
		Limit:    2,
		Window:   150 * time.Millisecond,
		Strategy: "sliding_window",
		Member:   "req-1",
		Now:      base,
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
		Now:      base.Add(10 * time.Millisecond),
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
		Now:      base.Add(20 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("third allow: %v", err)
	}
	if third.Allowed || third.Remaining != 0 {
		t.Fatalf("expected third request rejected with remaining=0, got %+v", third)
	}
	if !third.ResetAt.After(base) {
		t.Fatalf("expected reset time in the future, got %s", third.ResetAt)
	}

	afterReset, err := store.Allow(ctx, ratelimit.Request{
		Key:      key,
		Limit:    2,
		Window:   150 * time.Millisecond,
		Strategy: "sliding_window",
		Member:   "req-4",
		Now:      base.Add(175 * time.Millisecond),
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

	store := ratelimit.NewRedisStoreWithClient(client)
	key := ratelimit.Key(fmt.Sprintf("ip:atomic-%d", time.Now().UnixNano()), "/api/orders")
	ctx := context.Background()

	const (
		limit       = 25
		concurrency = 100
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

func TestRedisStoreRejectsInvalidRequests(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		request ratelimit.Request
		wantErr string
	}{
		{
			name: "missing client",
			request: ratelimit.Request{
				Key:    "rate_limit:{ip:test}:/api/orders",
				Member: "req-1",
				Window: time.Second,
			},
			wantErr: "redis client is not configured",
		},
		{
			name: "missing key",
			request: ratelimit.Request{
				Limit:  1,
				Member: "req-1",
				Window: time.Second,
			},
			wantErr: "rate limit key is required",
		},
		{
			name: "missing member",
			request: ratelimit.Request{
				Key:    "rate_limit:{ip:test}:/api/orders",
				Limit:  1,
				Window: time.Second,
			},
			wantErr: "rate limit member is required",
		},
		{
			name: "invalid window",
			request: ratelimit.Request{
				Key:    "rate_limit:{ip:test}:/api/orders",
				Limit:  1,
				Member: "req-1",
			},
			wantErr: "rate limit window must be greater than 0",
		},
		{
			name: "invalid limit",
			request: ratelimit.Request{
				Key:    "rate_limit:{ip:test}:/api/orders",
				Member: "req-1",
				Window: time.Second,
			},
			wantErr: "rate limit limit must be greater than 0",
		},
		{
			name: "unsupported strategy",
			request: ratelimit.Request{
				Key:      "rate_limit:{ip:test}:/api/orders",
				Limit:    1,
				Member:   "req-1",
				Window:   time.Second,
				Strategy: "token_bucket",
			},
			wantErr: `unsupported rate limit strategy "token_bucket"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := ratelimit.NewRedisStore(config.RedisConfig{})
			if tt.name != "missing client" {
				store = ratelimit.NewRedisStoreWithClient(redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"}))
			}

			_, err := store.Allow(ctx, tt.request)
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("expected error %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestRedisStoreHelpers(t *testing.T) {
	if store := ratelimit.NewRedisStore(config.RedisConfig{}); store == nil {
		t.Fatal("expected empty redis store instance, got nil")
	}

	if got := ratelimit.Key("user:{42}", "/api/orders"); got != "rate_limit:{user:_42_}:/api/orders" {
		t.Fatalf("unexpected sanitized key: %q", got)
	}
}

func TestRedisStoreUnexpectedScriptResponses(t *testing.T) {
	stub := &scriptStubClient{
		result: []any{"1", "0"},
	}
	store := ratelimit.NewRedisStoreWithClient(stub)

	_, err := store.Allow(context.Background(), ratelimit.Request{
		Key:      "rate_limit:{ip:test}:/api/orders",
		Limit:    1,
		Strategy: "sliding_window",
		Member:   "req-1",
		Window:   time.Second,
		Now:      time.Now(),
	})
	if err == nil || err.Error() != "unexpected rate limit script response length 2" {
		t.Fatalf("expected unexpected response length error, got %v", err)
	}

	stub = &scriptStubClient{
		result: []any{"bad", "0", "1"},
	}
	store = ratelimit.NewRedisStoreWithClient(stub)
	_, err = store.Allow(context.Background(), ratelimit.Request{
		Key:      "rate_limit:{ip:test}:/api/orders",
		Limit:    1,
		Strategy: "sliding_window",
		Member:   "req-1",
		Window:   time.Second,
		Now:      time.Now(),
	})
	if err == nil || !strings.Contains(err.Error(), "decode rate limit allowed flag") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestRedisStoreRoundsSubMillisecondWindowsUpToOneMillisecond(t *testing.T) {
	stub := &scriptStubClient{
		result: []any{int64(1), int64(0), time.Now().Add(time.Millisecond).UnixMilli()},
	}
	store := ratelimit.NewRedisStoreWithClient(stub)

	_, err := store.Allow(context.Background(), ratelimit.Request{
		Key:    "rate_limit:{ip:test}:/api/orders",
		Limit:  1,
		Member: "req-1",
		Window: 500 * time.Microsecond,
		Now:    time.Now(),
	})
	if err != nil {
		t.Fatalf("allow with sub-millisecond window: %v", err)
	}
	if len(stub.args) != 4 {
		t.Fatalf("expected 4 script args, got %d", len(stub.args))
	}
	if got := stub.args[1]; got != int64(1) {
		t.Fatalf("expected sub-millisecond window rounded up to 1ms, got %#v", got)
	}
}

type scriptStubClient struct {
	redis.UniversalClient
	result []any
	err    error
	args   []any
}

func (s *scriptStubClient) EvalSha(_ context.Context, _ string, _ []string, args ...any) *redis.Cmd {
	s.args = append([]any(nil), args...)
	cmd := redis.NewCmd(context.Background())
	if s.err != nil {
		cmd.SetErr(s.err)
		return cmd
	}
	cmd.SetVal(s.result)
	return cmd
}

func (s *scriptStubClient) ScriptExists(_ context.Context, _ ...string) *redis.BoolSliceCmd {
	cmd := redis.NewBoolSliceCmd(context.Background())
	cmd.SetVal([]bool{true})
	return cmd
}

func (s *scriptStubClient) ScriptLoad(_ context.Context, _ string) *redis.StringCmd {
	cmd := redis.NewStringCmd(context.Background())
	cmd.SetVal("sha")
	return cmd
}

func (s *scriptStubClient) Eval(_ context.Context, _ string, _ []string, _ ...any) *redis.Cmd {
	cmd := redis.NewCmd(context.Background())
	cmd.SetErr(errors.New("unexpected eval fallback"))
	return cmd
}
