package transport

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
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
					Err: os.NewSyscallError("connect", syscall.ECONNREFUSED),
				},
			},
			want: true,
		},
		{
			name: "dns lookup miss does not retry",
			err: &net.DNSError{
				Err: "no such host",
			},
			want: false,
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

func TestCaptureRequestBodyPreservesOriginalRequestWhenGetBodyIsAvailable(t *testing.T) {
	const payload = `{"id":"u-1","status":"retry"}`

	originalBody := &trackingReadCloser{Reader: strings.NewReader(payload)}
	req, err := http.NewRequest(http.MethodPut, "http://gateway/api/users", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Body = originalBody
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(payload)), nil
	}
	req.ContentLength = int64(len(payload))

	bufferedBody, hasBufferedBody, err := captureRequestBody(req, true)
	if err != nil {
		t.Fatalf("capture request body: %v", err)
	}
	if !hasBufferedBody {
		t.Fatal("expected request body to be buffered")
	}
	if string(bufferedBody) != payload {
		t.Fatalf("expected buffered body %q, got %q", payload, string(bufferedBody))
	}
	if !originalBody.closed {
		t.Fatal("expected original request body to be closed after buffering")
	}
	if req.Body != originalBody {
		t.Fatal("expected capture to leave the original request body pointer intact")
	}

	reader, err := req.GetBody()
	if err != nil {
		t.Fatalf("reopen request body: %v", err)
	}
	defer reader.Close()

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read reopened request body: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("expected reopened request body %q, got %q", payload, string(got))
	}
	if req.ContentLength != int64(len(payload)) {
		t.Fatalf("expected content length %d, got %d", len(payload), req.ContentLength)
	}
}

type cancelContextKey struct{}

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return nil
}
