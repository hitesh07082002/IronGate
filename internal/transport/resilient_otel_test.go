package transport

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/hitesh07082002/irongate/internal/middleware"
	"github.com/hitesh07082002/irongate/internal/transport/circuitbreaker"
)

func TestCBOpenSpan(t *testing.T) {
	route := &config.RouteConfig{
		Path:         "/api/users",
		Service:      "user-service",
		LoadBalancer: "round_robin",
		Retry: config.RetryConfig{
			MaxAttempts: 1,
		},
		Targets: []config.Target{
			{Host: "user-service-1", Port: 8081},
		},
	}
	breakerConfig := config.CBConfig{
		FailureThreshold:    5,
		SuccessThreshold:    1,
		Timeout:             time.Minute,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 1,
	}
	breakers := circuitbreaker.NewRegistry(breakerConfig, nil)

	prewarmTransport := NewResilientTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body:       http.NoBody,
			Request:    req,
		}, nil
	}), []config.RouteConfig{*route}, breakerConfig, nil, breakers, nil)
	for range 5 {
		req := newTransportRequest(t, route)
		resp, err := prewarmTransport.RoundTrip(req)
		if err != nil {
			t.Fatalf("prewarm round trip: %v", err)
		}
		resp.Body.Close()
	}

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	transport := NewResilientTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body:       http.NoBody,
			Request:    req,
		}, nil
	}), []config.RouteConfig{*route}, breakerConfig, nil, breakers, tp.Tracer("irongate.transport"))

	req := newTransportRequest(t, route)
	ctx, rootSpan := tp.Tracer("irongate.middleware.tracing").Start(req.Context(), "irongate.request")
	_, err := transport.RoundTrip(req.WithContext(ctx))
	if !errors.Is(err, ErrNoHealthyTargets) {
		t.Fatalf("expected ErrNoHealthyTargets after open circuit, got %v", err)
	}
	rootSpan.SetStatus(codes.Error, err.Error())
	rootSpan.End()

	root := findTransportSpanByName(t, recorder.Ended(), "irongate.request")
	if root.Status().Code != codes.Error {
		t.Fatalf("expected root span status error, got %s", root.Status().Code)
	}

	cbSpan := findTransportSpanByName(t, recorder.Ended(), "irongate.transport.circuitbreaker")
	if got := transportSpanAttribute(cbSpan.Attributes(), "cb.state"); got != "open" {
		t.Fatalf("expected cb.state open, got %v", got)
	}
	if !transportSpanHasEvent(cbSpan, "circuit_rejected") {
		t.Fatal("expected circuit_rejected event on circuit breaker span")
	}
	if countTransportSpansByName(recorder.Ended(), "irongate.transport.upstream") != 0 {
		t.Fatal("expected no upstream span when the circuit is already open")
	}
}

func TestRetrySpan_Waterfall(t *testing.T) {
	route := &config.RouteConfig{
		Path:         "/api/orders",
		Service:      "order-service",
		LoadBalancer: "round_robin",
		Retry: config.RetryConfig{
			MaxAttempts: 2,
			BaseDelay:   10 * time.Millisecond,
			MaxDelay:    10 * time.Millisecond,
			Jitter:      "none",
		},
		Targets: []config.Target{
			{Host: "order-service-1", Port: 8081},
			{Host: "order-service-2", Port: 8082},
		},
	}
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	var target1Calls int

	transport := NewResilientTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "order-service-1:8081":
			target1Calls++
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     make(http.Header),
				Body:       http.NoBody,
				Request:    req,
			}, nil
		case "order-service-2:8082":
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       http.NoBody,
				Request:    req,
			}, nil
		default:
			t.Fatalf("unexpected target %q", req.URL.Host)
			return nil, nil
		}
	}), []config.RouteConfig{*route}, config.CBConfig{}, nil, nil, tp.Tracer("irongate.transport"))

	resp, err := transport.RoundTrip(newTransportRequest(t, route))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	resp.Body.Close()
	if target1Calls != 1 {
		t.Fatalf("expected first target to be hit once, got %d", target1Calls)
	}

	attempt1 := findTransportSpanWithIntAttribute(t, recorder.Ended(), "irongate.transport.retry.attempt", "retry.attempt", 1)
	if got := transportSpanAttribute(attempt1.Attributes(), "retry.reason"); got != "upstream_5xx" {
		t.Fatalf("expected first retry attempt reason upstream_5xx, got %v", got)
	}
	findTransportSpanWithIntAttribute(t, recorder.Ended(), "irongate.transport.retry.backoff", "retry.attempt", 1)

	selectedTargets := collectTransportSpanAttributes(recorder.Ended(), "irongate.transport.loadbalancer", "lb.selected")
	if len(selectedTargets) != 2 {
		t.Fatalf("expected two load balancer selections, got %v", selectedTargets)
	}
	if selectedTargets[0] == selectedTargets[1] {
		t.Fatalf("expected retry to hit a different target, got %v", selectedTargets)
	}
}

func TestTransportSpansPropagateContextHierarchy(t *testing.T) {
	route := &config.RouteConfig{
		Path:         "/api/orders",
		Service:      "order-service",
		LoadBalancer: "round_robin",
		Retry: config.RetryConfig{
			MaxAttempts: 1,
		},
		Targets: []config.Target{
			{Host: "order-service-1", Port: 8081},
		},
	}
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	transport := NewResilientTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       http.NoBody,
			Request:    req,
		}, nil
	}), []config.RouteConfig{*route}, config.CBConfig{}, nil, nil, tp.Tracer("irongate.transport"))

	resp, err := transport.RoundTrip(newTransportRequest(t, route))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	resp.Body.Close()

	proxy := findTransportSpanByName(t, recorder.Ended(), "irongate.proxy")
	attempt := findTransportSpanWithIntAttribute(t, recorder.Ended(), "irongate.transport.retry.attempt", "retry.attempt", 1)
	loadBalancer := findTransportSpanByName(t, recorder.Ended(), "irongate.transport.loadbalancer")
	circuitBreaker := findTransportSpanByName(t, recorder.Ended(), "irongate.transport.circuitbreaker")
	upstream := findTransportSpanByName(t, recorder.Ended(), "irongate.transport.upstream")

	assertTransportParent(t, attempt, proxy)
	assertTransportParent(t, loadBalancer, attempt)
	assertTransportParent(t, circuitBreaker, loadBalancer)
	assertTransportParent(t, upstream, circuitBreaker)
}

func newTransportRequest(t *testing.T, route *config.RouteConfig) *http.Request {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, "http://gateway"+route.Path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	return req.WithContext(context.WithValue(req.Context(), middleware.RouteConfigKey, route))
}

func findTransportSpanByName(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()

	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}

	t.Fatalf("span %q not found", name)
	return nil
}

func assertTransportParent(t *testing.T, child, parent sdktrace.ReadOnlySpan) {
	t.Helper()

	if child.Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Fatalf("expected %s parent %s, got %s", child.Name(), parent.Name(), child.Parent().SpanID())
	}
}

func findTransportSpanWithIntAttribute(t *testing.T, spans []sdktrace.ReadOnlySpan, name, key string, want int64) sdktrace.ReadOnlySpan {
	t.Helper()

	for _, span := range spans {
		if span.Name() != name {
			continue
		}
		if got := transportSpanAttribute(span.Attributes(), key); got == want {
			return span
		}
	}

	t.Fatalf("span %q with %s=%d not found", name, key, want)
	return nil
}

func countTransportSpansByName(spans []sdktrace.ReadOnlySpan, name string) int {
	count := 0
	for _, span := range spans {
		if span.Name() == name {
			count++
		}
	}
	return count
}

func transportSpanAttribute(attrs []attribute.KeyValue, key string) any {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value.AsInterface()
		}
	}

	return nil
}

func collectTransportSpanAttributes(spans []sdktrace.ReadOnlySpan, name, key string) []string {
	values := make([]string, 0)
	for _, span := range spans {
		if span.Name() != name {
			continue
		}
		value, ok := transportSpanAttribute(span.Attributes(), key).(string)
		if !ok || value == "" {
			panic("missing string attribute " + key + " on span " + span.Name())
		}
		values = append(values, value)
	}
	return values
}

func transportSpanHasEvent(span sdktrace.ReadOnlySpan, eventName string) bool {
	for _, event := range span.Events() {
		if event.Name == eventName {
			return true
		}
	}

	return false
}
