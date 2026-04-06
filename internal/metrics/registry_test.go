package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
)

func TestRegistryUsesServiceOnlyLabels(t *testing.T) {
	registry := NewRegistry()

	registry.ObserveRequest("user-service", 200, 25*time.Millisecond)
	registry.IncRateLimitRejection("user-service")
	registry.IncRetry("user-service")
	registry.ObserveRetryDelay("user-service", 10*time.Millisecond)
	registry.IncCircuitOpen("user-service")
	registry.SetOpenCircuits("user-service", 1)
	registry.ObserveUpstreamDuration("user-service", 15*time.Millisecond)
	registry.IncInFlight("user-service")
	registry.DecInFlight("user-service")

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	checked := map[string]struct{}{
		MetricRequestsTotal:        {},
		MetricRequestFailuresTotal: {},
		MetricRequestDuration:      {},
		MetricRateLimitRejections:  {},
		MetricRetriesTotal:         {},
		MetricRetryDelay:           {},
		MetricCircuitOpensTotal:    {},
		MetricOpenCircuits:         {},
		MetricUpstreamDuration:     {},
		MetricInFlightRequests:     {},
	}

	for _, family := range families {
		if _, ok := checked[family.GetName()]; !ok {
			continue
		}

		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if label.GetName() != serviceLabel {
					t.Fatalf("expected only %q label on %s, found %q", serviceLabel, family.GetName(), label.GetName())
				}
			}
		}
	}
}

func TestObserveRequestCountsOnly5xxAsFailures(t *testing.T) {
	registry := NewRegistry()

	registry.ObserveRequest("payment-service", 429, 5*time.Millisecond)
	registry.ObserveRequest("payment-service", 503, 5*time.Millisecond)

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	if got := counterValueForService(t, families, MetricRequestsTotal, "payment-service"); got != 2 {
		t.Fatalf("expected 2 total requests, got %v", got)
	}
	if got := counterValueForService(t, families, MetricRequestFailuresTotal, "payment-service"); got != 1 {
		t.Fatalf("expected only 5xx requests to count as failures, got %v", got)
	}
}

func TestHandlerServesOpenMetricsExemplars(t *testing.T) {
	registry := NewRegistry()
	traceID := "0123456789abcdef0123456789abcdef"
	registry.ObserveRequestWithExemplar("payment-service", http.StatusOK, 5*time.Millisecond, traceID)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Accept", "application/openmetrics-text")
	recorder := httptest.NewRecorder()
	registry.Handler().ServeHTTP(recorder, req)

	if got := recorder.Header().Get("Content-Type"); !strings.Contains(got, "application/openmetrics-text") {
		t.Fatalf("expected OpenMetrics content type, got %q", got)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `traceID="`+traceID+`"`) {
		t.Fatalf("expected exemplar traceID in metrics output, got %s", body)
	}
}

func TestZeroValueRegistryHandlerReturnsNotFound(t *testing.T) {
	var registry Registry

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	recorder := httptest.NewRecorder()
	registry.Handler().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected zero-value registry handler to return 404, got %d", recorder.Code)
	}
}

func TestZeroValueRegistryGatherReturnsNilFamilies(t *testing.T) {
	var registry Registry

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("expected nil error from zero-value registry gather, got %v", err)
	}
	if families != nil {
		t.Fatalf("expected nil metric families from zero-value registry, got %v", families)
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
			if labelValue(metric, serviceLabel) == service {
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
