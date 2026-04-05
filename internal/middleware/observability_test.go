package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/hitesh07082002/irongate/internal/metrics"
	"github.com/hitesh07082002/irongate/internal/ratelimit"
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
	handler := RateLimiterWithMetrics(store, testRateLimitLogger(), registry, RateLimiterOptions{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
