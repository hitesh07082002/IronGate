package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/hitesh07082002/irongate/internal/middleware"
	"github.com/hitesh07082002/irongate/internal/transport/circuitbreaker"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestLoadBalancerTransportInitializesNilResponseHeaders(t *testing.T) {
	transport := NewResilientTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
			Request:    req,
		}, nil
	}), config.CBConfig{})

	route := &config.RouteConfig{
		Path:         "/api/users",
		Service:      "user-service",
		LoadBalancer: "round_robin",
		Targets: []config.Target{
			{Host: "user-service-1", Port: 8081},
		},
	}

	req, err := http.NewRequest(http.MethodGet, "http://gateway/api/users", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req = req.WithContext(context.WithValue(req.Context(), middleware.RouteConfigKey, route))

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if resp.Header == nil {
		t.Fatal("expected transport to initialize response headers")
	}
	if got := resp.Header.Get(HeaderServedBy); got != "user-service-1:8081" {
		t.Fatalf("expected %q, got %q", "user-service-1:8081", got)
	}
}

func TestCircuitBreakerTransportDelaysSuccessUntilBodyEOF(t *testing.T) {
	registry := circuitbreaker.NewRegistry(config.CBConfig{
		FailureThreshold:    1,
		SuccessThreshold:    1,
		Timeout:             10 * time.Millisecond,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 1,
	})
	target := "user-service-1:8081"
	breaker := registry.Breaker(target)

	if !breaker.Allow() {
		t.Fatal("expected closed breaker to allow request")
	}
	breaker.RecordFailure()
	waitForBreakerState(t, breaker, circuitbreaker.StateHalfOpen, 200*time.Millisecond)

	transport := &CircuitBreakerTransport{
		next: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("ok")),
				Request:    req,
			}, nil
		}),
		registry: registry,
	}

	req, err := http.NewRequest(http.MethodGet, "http://"+target+"/health", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if breaker.State() != circuitbreaker.StateHalfOpen {
		t.Fatalf("expected breaker to remain half-open until body EOF, got %s", breaker.State())
	}

	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close body: %v", err)
	}
	if breaker.State() != circuitbreaker.StateClosed {
		t.Fatalf("expected breaker to close after clean body EOF, got %s", breaker.State())
	}
}

func TestCircuitBreakerTransportReopensOnBodyReadError(t *testing.T) {
	registry := circuitbreaker.NewRegistry(config.CBConfig{
		FailureThreshold:    1,
		SuccessThreshold:    1,
		Timeout:             10 * time.Millisecond,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 1,
	})
	target := "user-service-1:8081"
	breaker := registry.Breaker(target)

	if !breaker.Allow() {
		t.Fatal("expected closed breaker to allow request")
	}
	breaker.RecordFailure()
	waitForBreakerState(t, breaker, circuitbreaker.StateHalfOpen, 200*time.Millisecond)

	transport := &CircuitBreakerTransport{
		next: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       &errOnReadCloser{err: io.ErrUnexpectedEOF},
				Request:    req,
			}, nil
		}),
		registry: registry,
	}

	req, err := http.NewRequest(http.MethodGet, "http://"+target+"/health", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if breaker.State() != circuitbreaker.StateHalfOpen {
		t.Fatalf("expected breaker to remain half-open before the body read, got %s", breaker.State())
	}

	_, err = io.ReadAll(resp.Body)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected body read error, got %v", err)
	}
	if breaker.State() != circuitbreaker.StateOpen {
		t.Fatalf("expected breaker to reopen after body read error, got %s", breaker.State())
	}
}

func TestCountsTowardCircuitIgnoresCallerDeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("expected expired context, got %v", ctx.Err())
	}
	if countsTowardCircuit(ctx, context.DeadlineExceeded) {
		t.Fatal("expected caller deadline to be ignored by the circuit breaker")
	}
	if !countsTowardCircuit(context.Background(), context.DeadlineExceeded) {
		t.Fatal("expected upstream deadline to count toward the circuit breaker")
	}
}

type errOnReadCloser struct {
	err error
}

func (r *errOnReadCloser) Read(_ []byte) (int, error) {
	return 0, r.err
}

func (r *errOnReadCloser) Close() error {
	return nil
}

func waitForBreakerState(t *testing.T, breaker *circuitbreaker.Breaker, want circuitbreaker.State, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		if breaker.State() == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected breaker state %s within %v, got %s", want, timeout, breaker.State())
		}
		time.Sleep(time.Millisecond)
	}
}
