package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/hitesh07082002/irongate/internal/metrics"
	"github.com/hitesh07082002/irongate/internal/ratelimit"
	"github.com/hitesh07082002/irongate/internal/telemetry"
)

func TestRateLimiterRecordsRejectionMetric(t *testing.T) {
	registry := metrics.NewRegistry()
	store := &stubRateLimitStore{
		decision: ratelimit.Decision{
			Allowed:   false,
			Remaining: 0,
			ResetAt:   time.Now().Add(time.Minute),
		},
	}

	nextCalled := false
	handler := RateLimiterWithMetrics(store, testRateLimitLogger(), registry, RateLimiterOptions{}, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := rateLimitedRequest(t)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if nextCalled {
		t.Fatal("expected rejection to stop before next handler")
	}
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", recorder.Code)
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	if got := counterValueForService(t, families, metrics.MetricRateLimitRejections, "order-service"); got != 1 {
		t.Fatalf("expected one rate-limit rejection, got %v", got)
	}
}

func TestRateLimiterSpanRecordsRejectedAttributes(t *testing.T) {
	registry := metrics.NewRegistry()
	store := &stubRateLimitStore{
		decision: ratelimit.Decision{
			Allowed:   false,
			Remaining: 0,
			ResetAt:   time.Now().Add(time.Minute),
		},
	}
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	handler := RateLimiterWithMetrics(store, testRateLimitLogger(), registry, RateLimiterOptions{}, tp.Tracer("irongate.middleware.ratelimiter"))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("expected rate limiter rejection to stop before next handler")
	}))

	req := rateLimitedRequest(t)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	span := findEndedSpanByName(t, recorder.Ended(), "irongate.middleware.ratelimiter")
	if got := spanAttribute(span.Attributes(), "ratelimit.outcome"); got != "rejected" {
		t.Fatalf("expected ratelimit.outcome rejected, got %v", got)
	}
	if got := spanAttribute(span.Attributes(), "ratelimit.remaining"); got != int64(0) {
		t.Fatalf("expected ratelimit.remaining 0, got %v", got)
	}
	if got := spanAttribute(span.Attributes(), "ratelimit.client_key"); got != telemetry.HashAttr("ip:127.0.0.1") {
		t.Fatalf("expected hashed ratelimit.client_key, got %v", got)
	}
	if span.Status().Code != codes.Error {
		t.Fatalf("expected rate limiter span status error, got %s", span.Status().Code)
	}
}

func TestMetricsMiddlewareRecordsExemplarForSampledSpan(t *testing.T) {
	registry := metrics.NewRegistry()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))

	handler := Metrics(registry)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/orders", nil)
	req = req.WithContext(context.WithValue(req.Context(), RouteConfigKey, &config.RouteConfig{
		Path:    "/api/orders",
		Service: "order-service",
	}))
	ctx, span := tp.Tracer("irongate.middleware.metrics").Start(req.Context(), "irongate.request")
	req = req.WithContext(ctx)

	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	traceID := span.SpanContext().TraceID().String()
	span.End()

	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsReq.Header.Set("Accept", "application/openmetrics-text")
	metricsResp := httptest.NewRecorder()
	registry.Handler().ServeHTTP(metricsResp, metricsReq)

	if body := metricsResp.Body.String(); !strings.Contains(body, `traceID="`+traceID+`"`) {
		t.Fatalf("expected exemplar traceID %s in metrics output, got %s", traceID, body)
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
