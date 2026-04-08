package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	dashboardRangeWindow = 2 * time.Minute
	dashboardRangeStep   = 10 * time.Second
)

type landingDashboardResponse struct {
	InFlight       json.RawMessage `json:"in_flight"`
	TotalRPS       json.RawMessage `json:"total_rps"`
	ErrorRPS       json.RawMessage `json:"error_rps"`
	RequestsServed json.RawMessage `json:"requests_served"`
	CircuitEvents  json.RawMessage `json:"circuit_events"`
	RateLimited    json.RawMessage `json:"rate_limited"`
}

type chaosDashboardResponse struct {
	RequestRate      json.RawMessage `json:"request_rate"`
	ErrorRate        json.RawMessage `json:"error_rate"`
	LatencyP50       json.RawMessage `json:"latency_p50"`
	LatencyP95       json.RawMessage `json:"latency_p95"`
	LatencyP99       json.RawMessage `json:"latency_p99"`
	CircuitState     json.RawMessage `json:"circuit_state"`
	TotalRate        json.RawMessage `json:"total_rate"`
	RejectedRate     json.RawMessage `json:"rejected_rate"`
	SuccessCount     json.RawMessage `json:"success_count"`
	ErrorCount       json.RawMessage `json:"error_count"`
	RetryCount       json.RawMessage `json:"retry_count"`
	RateLimitedCount json.RawMessage `json:"rate_limited_count"`
}

func (a *app) handleLandingDashboard(w http.ResponseWriter, r *http.Request) {
	inFlight, err := a.fetchPrometheusInstant(r.Context(), "sum(gateway_in_flight_requests)")
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	totalRPS, err := a.fetchPrometheusInstant(r.Context(), "sum(rate(gateway_requests_total[1m]))")
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	errorRPS, err := a.fetchPrometheusInstant(r.Context(), "sum(rate(gateway_request_failures_total[1m]))")
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	requestsServed, err := a.fetchPrometheusInstant(r.Context(), "sum(gateway_requests_total)")
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	circuitEvents, err := a.fetchPrometheusInstant(r.Context(), "sum(gateway_circuit_opens_total)")
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	rateLimited, err := a.fetchPrometheusInstant(r.Context(), "sum(gateway_rate_limit_rejections_total)")
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, landingDashboardResponse{
		InFlight:       inFlight,
		TotalRPS:       totalRPS,
		ErrorRPS:       errorRPS,
		RequestsServed: requestsServed,
		CircuitEvents:  circuitEvents,
		RateLimited:    rateLimited,
	})
}

func (a *app) handleChaosDashboard(w http.ResponseWriter, r *http.Request) {
	requestRate, err := a.fetchPrometheusRange(r.Context(), "sum(rate(gateway_requests_total[1m]))", dashboardRangeWindow, dashboardRangeStep)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	errorRate, err := a.fetchPrometheusRange(r.Context(), "sum(rate(gateway_request_failures_total[1m]))", dashboardRangeWindow, dashboardRangeStep)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	latencyP50, err := a.fetchPrometheusRange(r.Context(), "histogram_quantile(0.50, sum by (le) (rate(gateway_request_duration_seconds_bucket[1m])))", dashboardRangeWindow, dashboardRangeStep)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	latencyP95, err := a.fetchPrometheusRange(r.Context(), "histogram_quantile(0.95, sum by (le) (rate(gateway_request_duration_seconds_bucket[1m])))", dashboardRangeWindow, dashboardRangeStep)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	latencyP99, err := a.fetchPrometheusRange(r.Context(), "histogram_quantile(0.99, sum by (le) (rate(gateway_request_duration_seconds_bucket[1m])))", dashboardRangeWindow, dashboardRangeStep)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	circuitState, err := a.fetchPrometheusRange(r.Context(), "gateway_circuit_state{service=~\".+\"}", dashboardRangeWindow, dashboardRangeStep)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	totalRate, err := a.fetchPrometheusRange(r.Context(), "sum(rate(gateway_requests_total[1m]))", dashboardRangeWindow, dashboardRangeStep)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	rejectedRate, err := a.fetchPrometheusRange(r.Context(), "sum(rate(gateway_rate_limit_rejections_total[1m]))", dashboardRangeWindow, dashboardRangeStep)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	successCount, err := a.fetchPrometheusInstant(r.Context(), "sum(increase(gateway_requests_total[1m]))")
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	errorCount, err := a.fetchPrometheusInstant(r.Context(), "sum(increase(gateway_request_failures_total[1m]))")
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	retryCount, err := a.fetchPrometheusInstant(r.Context(), "sum(increase(gateway_retries_total[1m]))")
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	rateLimitedCount, err := a.fetchPrometheusInstant(r.Context(), "sum(increase(gateway_rate_limit_rejections_total[1m]))")
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, chaosDashboardResponse{
		RequestRate:      requestRate,
		ErrorRate:        errorRate,
		LatencyP50:       latencyP50,
		LatencyP95:       latencyP95,
		LatencyP99:       latencyP99,
		CircuitState:     circuitState,
		TotalRate:        totalRate,
		RejectedRate:     rejectedRate,
		SuccessCount:     successCount,
		ErrorCount:       errorCount,
		RetryCount:       retryCount,
		RateLimitedCount: rateLimitedCount,
	})
}

func (a *app) fetchPrometheusInstant(ctx context.Context, query string) (json.RawMessage, error) {
	params := url.Values{}
	params.Set("query", query)
	return a.fetchPrometheusJSON(ctx, "http://prometheus:9090/api/v1/query", params)
}

func (a *app) fetchPrometheusRange(ctx context.Context, query string, window time.Duration, step time.Duration) (json.RawMessage, error) {
	now := time.Now().UTC()
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", fmt.Sprintf("%d", now.Add(-window).Unix()))
	params.Set("end", fmt.Sprintf("%d", now.Unix()))
	params.Set("step", fmt.Sprintf("%ds", int(step/time.Second)))
	return a.fetchPrometheusJSON(ctx, "http://prometheus:9090/api/v1/query_range", params)
}

func (a *app) fetchPrometheusJSON(ctx context.Context, endpoint string, params url.Values) (json.RawMessage, error) {
	query := strings.TrimSpace(params.Get("query"))
	if !allowedPrometheusQuery(query) {
		return nil, fmt.Errorf("query not permitted")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build prometheus request: %w", err)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query prometheus: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read prometheus response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		return nil, fmt.Errorf("prometheus query failed: %s", message)
	}

	return json.RawMessage(body), nil
}
