package transport

import (
	"context"
	"math/rand"
	"net/http"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/hitesh07082002/irongate/internal/metrics"
	"github.com/hitesh07082002/irongate/internal/middleware"
	"github.com/hitesh07082002/irongate/internal/transport/circuitbreaker"
)

func TestRetryTransportRecordsRetryMetrics(t *testing.T) {
	registry := metrics.NewRegistry()
	var calls int

	transport := newRetryTransport(
		roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			statusCode := http.StatusServiceUnavailable
			if calls == 2 {
				statusCode = http.StatusOK
			}

			return &http.Response{
				StatusCode: statusCode,
				Header:     make(http.Header),
				Body:       http.NoBody,
				Request:    req,
			}, nil
		}),
		func(context.Context, time.Duration) error { return nil },
		rand.New(rand.NewSource(1)),
		registry,
		nil,
	)

	req, err := http.NewRequest(http.MethodGet, "http://gateway/api/orders", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req = req.WithContext(context.WithValue(req.Context(), middleware.RouteConfigKey, &config.RouteConfig{
		Path:    "/api/orders",
		Service: "order-service",
		Retry: config.RetryConfig{
			MaxAttempts: 2,
			BaseDelay:   50 * time.Millisecond,
			MaxDelay:    50 * time.Millisecond,
			Jitter:      fullJitterStrategy,
		},
	}))

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected retry to recover with 200, got %d", resp.StatusCode)
	}
	if calls != 2 {
		t.Fatalf("expected two upstream attempts, got %d", calls)
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	if got := counterValueForService(t, families, metrics.MetricRetriesTotal, "order-service"); got != 1 {
		t.Fatalf("expected one recorded retry, got %v", got)
	}
	if got := histogramCountForService(t, families, metrics.MetricRetryDelay, "order-service"); got != 1 {
		t.Fatalf("expected one retry-delay sample, got %v", got)
	}
}

func TestCircuitBreakerTransportRecordsMetrics(t *testing.T) {
	registry := metrics.NewRegistry()
	breakers := circuitbreaker.NewRegistry(config.CBConfig{
		FailureThreshold:    1,
		SuccessThreshold:    1,
		Timeout:             10 * time.Millisecond,
		WindowSize:          time.Minute,
		HalfOpenMaxRequests: 1,
	}, nil)

	transport := &CircuitBreakerTransport{
		next: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     make(http.Header),
				Body:       http.NoBody,
				Request:    req,
			}, nil
		}),
		registry: breakers,
		metrics:  registry,
		tracer:   nil,
	}

	route := &config.RouteConfig{
		Path:    "/api/users",
		Service: "user-service",
		Targets: []config.Target{
			{Host: "user-service-1", Port: 8081},
		},
	}

	req, err := http.NewRequest(http.MethodGet, "http://user-service-1:8081/health", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req = req.WithContext(context.WithValue(req.Context(), middleware.RouteConfigKey, route))

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 to open the breaker, got %d", resp.StatusCode)
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	if got := counterValueForService(t, families, metrics.MetricCircuitOpensTotal, "user-service"); got != 1 {
		t.Fatalf("expected one circuit-open event, got %v", got)
	}
	if got := gaugeValueForService(t, families, metrics.MetricOpenCircuits, "user-service"); got != 1 {
		t.Fatalf("expected one open circuit, got %v", got)
	}
	if got := histogramCountForService(t, families, metrics.MetricUpstreamDuration, "user-service"); got != 1 {
		t.Fatalf("expected one upstream-duration sample, got %v", got)
	}
}

func counterValueForService(t *testing.T, families []*dto.MetricFamily, name, service string) float64 {
	t.Helper()

	metric := metricForService(t, families, name, service)
	if metric.Counter == nil {
		t.Fatalf("metric %s for service %s is not a counter", name, service)
	}

	return metric.GetCounter().GetValue()
}

func gaugeValueForService(t *testing.T, families []*dto.MetricFamily, name, service string) float64 {
	t.Helper()

	metric := metricForService(t, families, name, service)
	if metric.Gauge == nil {
		t.Fatalf("metric %s for service %s is not a gauge", name, service)
	}

	return metric.GetGauge().GetValue()
}

func histogramCountForService(t *testing.T, families []*dto.MetricFamily, name, service string) uint64 {
	t.Helper()

	metric := metricForService(t, families, name, service)
	if metric.Histogram == nil {
		t.Fatalf("metric %s for service %s is not a histogram", name, service)
	}

	return metric.GetHistogram().GetSampleCount()
}

func metricForService(t *testing.T, families []*dto.MetricFamily, name, service string) *dto.Metric {
	t.Helper()

	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			if labelValue(metric, "service") == service {
				return metric
			}
		}
	}

	t.Fatalf("metric %s for service %s not found", name, service)
	return nil
}

func labelValue(metric *dto.Metric, labelName string) string {
	for _, label := range metric.Label {
		if label.GetName() == labelName {
			return label.GetValue()
		}
	}

	return ""
}
