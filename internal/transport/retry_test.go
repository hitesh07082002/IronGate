package transport

import (
	"context"
	"errors"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/hitesh07082002/irongate/internal/middleware"
)

func TestNormalizeRetryPolicyDisablesRetriesForNonIdempotentMethods(t *testing.T) {
	policy := normalizeRetryPolicy(config.RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   time.Millisecond,
		MaxDelay:    5 * time.Millisecond,
		Jitter:      fullJitterStrategy,
	}, false)

	if policy.maxAttempts != 1 {
		t.Fatalf("expected non-idempotent methods to disable retries, got %d attempts", policy.maxAttempts)
	}
}

func TestIsTransientErrorClassifiesExpectedFailures(t *testing.T) {
	testCases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "deadline exceeded retries",
			err:  context.DeadlineExceeded,
			want: true,
		},
		{
			name: "dns timeout retries",
			err: &net.DNSError{
				IsTimeout: true,
			},
			want: true,
		},
		{
			name: "wrapped network error retries",
			err: &url.Error{
				Err: &net.OpError{
					Op:  "dial",
					Net: "tcp",
					Err: errors.New("connection refused"),
				},
			},
			want: true,
		},
		{
			name: "context canceled does not retry",
			err:  context.Canceled,
			want: false,
		},
		{
			name: "open circuit does not retry",
			err:  ErrCircuitOpen,
			want: false,
		},
		{
			name: "no healthy targets does not retry",
			err:  ErrNoHealthyTargets,
			want: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := isTransientError(testCase.err); got != testCase.want {
				t.Fatalf("expected %t, got %t for %v", testCase.want, got, testCase.err)
			}
		})
	}
}

func TestRetryTransportStopsWhenContextCancelsDuringBackoff(t *testing.T) {
	var calls int
	transport := newRetryTransport(
		roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     make(http.Header),
				Body:       http.NoBody,
				Request:    req,
			}, nil
		}),
		func(ctx context.Context, _ time.Duration) error {
			cancel := ctx.Value(cancelContextKey{}).(context.CancelFunc)
			cancel()
			<-ctx.Done()
			return ctx.Err()
		},
		rand.New(rand.NewSource(1)),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = context.WithValue(ctx, cancelContextKey{}, cancel)

	req, err := http.NewRequest(http.MethodGet, "http://gateway/api/orders", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req = req.WithContext(context.WithValue(ctx, middleware.RouteConfigKey, &config.RouteConfig{
		Path:    "/api/orders",
		Service: "order-service",
		Retry: config.RetryConfig{
			MaxAttempts: 2,
			BaseDelay:   50 * time.Millisecond,
			MaxDelay:    50 * time.Millisecond,
			Jitter:      fullJitterStrategy,
		},
	}))

	_, err = transport.RoundTrip(req)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected retry loop to stop before second attempt, got %d calls", calls)
	}
}

type cancelContextKey struct{}
