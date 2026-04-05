package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/hitesh07082002/irongate/internal/middleware"
)

const (
	defaultRetryBaseDelay   = 100 * time.Millisecond
	defaultRetryMaxDelay    = 2 * time.Second
	maxRetryReplayBodyBytes = 1 << 20
	fullJitterStrategy      = "full"
)

type sleepFunc func(context.Context, time.Duration) error

type retryPolicy struct {
	maxAttempts int
	baseDelay   time.Duration
	maxDelay    time.Duration
	jitter      string
}

type RetryTransport struct {
	next  http.RoundTripper
	sleep sleepFunc

	randMu sync.Mutex
	rand   *rand.Rand
}

func NewRetryTransport(next http.RoundTripper) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}

	return newRetryTransport(
		next,
		sleepWithContext,
		rand.New(rand.NewSource(time.Now().UnixNano())),
	)
}

func newRetryTransport(next http.RoundTripper, sleep sleepFunc, rng *rand.Rand) *RetryTransport {
	if sleep == nil {
		sleep = sleepWithContext
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	return &RetryTransport{
		next:  next,
		sleep: sleep,
		rand:  rng,
	}
}

func (rt *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	route := middleware.GetRouteConfig(req)
	if route == nil {
		return nil, fmt.Errorf("route config missing from request context")
	}

	policy := normalizeRetryPolicy(route.Retry, isRetryableMethod(req.Method))
	bufferedBody, hasBufferedBody, err := captureRequestBody(req, policy.maxAttempts > 1)
	if err != nil {
		return nil, fmt.Errorf("capture request body: %w", err)
	}

	triedTargets := make(map[string]struct{})
	totalTargets := uniqueTargetCount(route.Targets)
	logicalAttempt := 0

	for {
		if err := req.Context().Err(); err != nil {
			return nil, &AttemptError{
				Err:        err,
				RetryCount: logicalAttempt,
			}
		}

		attemptReq, err := cloneAttemptRequest(req, bufferedBody, hasBufferedBody, logicalAttempt, triedTargets)
		if err != nil {
			return nil, err
		}

		resp, roundTripErr := rt.next.RoundTrip(attemptReq)
		metadata := resolveAttemptMetadata(resp, roundTripErr, logicalAttempt)

		if errors.Is(roundTripErr, ErrCircuitOpen) {
			if metadata.target != "" {
				triedTargets[metadata.target] = struct{}{}
			}
			if totalTargets == 0 || len(triedTargets) >= totalTargets {
				return nil, &AttemptError{
					Err:        ErrNoHealthyTargets,
					RetryCount: logicalAttempt,
					Target:     metadata.target,
				}
			}
			if err := req.Context().Err(); err != nil {
				return nil, &AttemptError{
					Err:        err,
					RetryCount: logicalAttempt,
					Target:     metadata.target,
				}
			}
			continue
		}

		if !shouldRetry(policy, logicalAttempt, resp, roundTripErr) {
			return resp, roundTripErr
		}

		if metadata.target != "" {
			triedTargets[metadata.target] = struct{}{}
		}
		if len(triedTargets) >= totalTargets {
			clear(triedTargets)
		}

		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}

		delay := rt.nextDelay(policy, logicalAttempt)
		if err := rt.sleep(req.Context(), delay); err != nil {
			return nil, &AttemptError{
				Err:        err,
				RetryCount: logicalAttempt,
				Target:     metadata.target,
			}
		}

		logicalAttempt++
	}
}

func resolveAttemptMetadata(resp *http.Response, err error, retryCount int) attemptMetadata {
	if resp != nil && resp.Request != nil {
		metadata := getAttemptMetadata(resp.Request)
		if metadata.retryCount == 0 && retryCount > 0 {
			metadata.retryCount = retryCount
		}
		return metadata
	}

	if metadata, ok := metadataFromError(err); ok {
		if metadata.retryCount == 0 && retryCount > 0 {
			metadata.retryCount = retryCount
		}
		return metadata
	}

	return attemptMetadata{retryCount: retryCount}
}

func normalizeRetryPolicy(cfg config.RetryConfig, methodRetryable bool) retryPolicy {
	policy := retryPolicy{
		maxAttempts: cfg.MaxAttempts,
		baseDelay:   cfg.BaseDelay,
		maxDelay:    cfg.MaxDelay,
		jitter:      cfg.Jitter,
	}

	if policy.maxAttempts <= 0 {
		policy.maxAttempts = 1
	}
	if !methodRetryable {
		policy.maxAttempts = 1
	}
	if policy.baseDelay <= 0 {
		policy.baseDelay = defaultRetryBaseDelay
	}
	if policy.maxDelay <= 0 {
		policy.maxDelay = defaultRetryMaxDelay
	}
	if policy.maxDelay < policy.baseDelay {
		policy.maxDelay = policy.baseDelay
	}
	if policy.jitter == "" {
		policy.jitter = fullJitterStrategy
	}

	return policy
}

func shouldRetry(policy retryPolicy, logicalAttempt int, resp *http.Response, err error) bool {
	if logicalAttempt+1 >= policy.maxAttempts {
		return false
	}
	if err != nil {
		return isTransientError(err)
	}
	if resp == nil {
		return false
	}

	return isRetryableStatus(resp.StatusCode)
}

func isRetryableMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions:
		return true
	default:
		return false
	}
}

func isRetryableStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func isTransientError(err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, ErrCircuitOpen):
		return false
	case errors.Is(err, ErrNoHealthyTargets):
		return false
	case errors.Is(err, context.Canceled):
		return false
	case errors.Is(err, context.DeadlineExceeded):
		return true
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return isTransientError(urlErr.Err)
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.Timeout() || dnsErr.Temporary()
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) && isConnectionFailure(opErr.Err) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}

	return isConnectionFailure(err)
}

func captureRequestBody(req *http.Request, shouldBuffer bool) ([]byte, bool, error) {
	if !shouldBuffer || req == nil || req.Body == nil || req.Body == http.NoBody {
		return nil, false, nil
	}

	var (
		body []byte
		err  error
	)
	if req.GetBody != nil {
		reader, readErr := req.GetBody()
		if readErr != nil {
			return nil, false, readErr
		}
		body, err = io.ReadAll(io.LimitReader(reader, maxRetryReplayBodyBytes+1))
		if closeErr := reader.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
		if closeErr := req.Body.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	} else {
		body, err = io.ReadAll(io.LimitReader(req.Body, maxRetryReplayBodyBytes+1))
		if closeErr := req.Body.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}
	if err != nil {
		return nil, false, err
	}
	if len(body) > maxRetryReplayBodyBytes {
		return nil, false, fmt.Errorf("request body exceeds retry replay limit of %d bytes", maxRetryReplayBodyBytes)
	}

	return body, true, nil
}

func cloneAttemptRequest(req *http.Request, body []byte, hasBufferedBody bool, retryCount int, triedTargets map[string]struct{}) (*http.Request, error) {
	if req == nil {
		return nil, fmt.Errorf("request must not be nil")
	}

	attemptReq := req.Clone(req.Context())
	if hasBufferedBody {
		attemptReq.Body = io.NopCloser(bytes.NewReader(body))
		attemptReq.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
		attemptReq.ContentLength = int64(len(body))
		if len(body) == 0 {
			attemptReq.Body = http.NoBody
			attemptReq.GetBody = func() (io.ReadCloser, error) {
				return http.NoBody, nil
			}
		}
	}

	metadata := getAttemptMetadata(attemptReq).
		withRetryCount(retryCount).
		withTarget("").
		withTriedTargets(triedTargets)
	return withAttemptMetadata(attemptReq, metadata), nil
}

func (rt *RetryTransport) nextDelay(policy retryPolicy, logicalAttempt int) time.Duration {
	delay := policy.baseDelay
	for range logicalAttempt {
		if delay >= policy.maxDelay {
			delay = policy.maxDelay
			break
		}
		if delay > policy.maxDelay/2 {
			delay = policy.maxDelay
			break
		}
		delay *= 2
	}
	if delay > policy.maxDelay {
		delay = policy.maxDelay
	}
	if delay <= 0 {
		return 0
	}
	if policy.jitter != fullJitterStrategy {
		return delay
	}

	rt.randMu.Lock()
	defer rt.randMu.Unlock()

	return time.Duration(rt.rand.Int63n(int64(delay) + 1))
}

func uniqueTargetCount(targets []config.Target) int {
	unique := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		unique[targetAddress(target)] = struct{}{}
	}
	return len(unique)
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func targetAddress(target config.Target) string {
	return net.JoinHostPort(target.Host, strconv.Itoa(target.Port))
}

func isConnectionFailure(err error) bool {
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return true
	case errors.Is(err, syscall.ECONNRESET):
		return true
	case errors.Is(err, syscall.EPIPE):
		return true
	case errors.Is(err, syscall.ETIMEDOUT):
		return true
	case errors.Is(err, syscall.EHOSTUNREACH):
		return true
	case errors.Is(err, syscall.ENETUNREACH):
		return true
	default:
		return false
	}
}
