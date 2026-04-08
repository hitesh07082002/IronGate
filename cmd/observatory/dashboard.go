package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	dashboardRangeWindow  = 2 * time.Minute
	dashboardRangeStep    = 10 * time.Second
	dashboardQueryTimeout = 5 * time.Second
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
	end := time.Now().UTC().Truncate(time.Second)

	var (
		inFlight       json.RawMessage
		totalRPS       json.RawMessage
		errorRPS       json.RawMessage
		requestsServed json.RawMessage
		circuitEvents  json.RawMessage
		rateLimited    json.RawMessage
	)

	err := runDashboardTasks(r.Context(),
		func(ctx context.Context) error {
			var fetchErr error
			inFlight, fetchErr = a.fetchPrometheusInstantAt(ctx, "sum(gateway_in_flight_requests)", end)
			return fetchErr
		},
		func(ctx context.Context) error {
			var fetchErr error
			totalRPS, fetchErr = a.fetchPrometheusInstantAt(ctx, "sum(rate(gateway_requests_total[1m]))", end)
			return fetchErr
		},
		func(ctx context.Context) error {
			var fetchErr error
			errorRPS, fetchErr = a.fetchPrometheusInstantAt(ctx, "sum(rate(gateway_request_failures_total[1m]))", end)
			return fetchErr
		},
		func(ctx context.Context) error {
			var fetchErr error
			requestsServed, fetchErr = a.fetchPrometheusInstantAt(ctx, "sum(gateway_requests_total)", end)
			return fetchErr
		},
		func(ctx context.Context) error {
			var fetchErr error
			circuitEvents, fetchErr = a.fetchPrometheusInstantAt(ctx, "sum(gateway_circuit_opens_total)", end)
			return fetchErr
		},
		func(ctx context.Context) error {
			var fetchErr error
			rateLimited, fetchErr = a.fetchPrometheusInstantAt(ctx, "sum(gateway_rate_limit_rejections_total)", end)
			return fetchErr
		},
	)
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
	end := time.Now().UTC().Truncate(dashboardRangeStep)
	start := end.Add(-dashboardRangeWindow)

	var (
		requestRate      json.RawMessage
		errorRate        json.RawMessage
		latencyP50       json.RawMessage
		latencyP95       json.RawMessage
		latencyP99       json.RawMessage
		circuitState     json.RawMessage
		rejectedRate     json.RawMessage
		successCount     json.RawMessage
		errorCount       json.RawMessage
		retryCount       json.RawMessage
		rateLimitedCount json.RawMessage
	)

	err := runDashboardTasks(r.Context(),
		func(ctx context.Context) error {
			var fetchErr error
			requestRate, fetchErr = a.fetchPrometheusRangeWindow(ctx, "sum(rate(gateway_requests_total[1m]))", start, end, dashboardRangeStep)
			return fetchErr
		},
		func(ctx context.Context) error {
			var fetchErr error
			errorRate, fetchErr = a.fetchPrometheusRangeWindow(ctx, "sum(rate(gateway_request_failures_total[1m]))", start, end, dashboardRangeStep)
			return fetchErr
		},
		func(ctx context.Context) error {
			var fetchErr error
			latencyP50, fetchErr = a.fetchPrometheusRangeWindow(ctx, "histogram_quantile(0.50, sum by (le) (rate(gateway_request_duration_seconds_bucket[1m])))", start, end, dashboardRangeStep)
			return fetchErr
		},
		func(ctx context.Context) error {
			var fetchErr error
			latencyP95, fetchErr = a.fetchPrometheusRangeWindow(ctx, "histogram_quantile(0.95, sum by (le) (rate(gateway_request_duration_seconds_bucket[1m])))", start, end, dashboardRangeStep)
			return fetchErr
		},
		func(ctx context.Context) error {
			var fetchErr error
			latencyP99, fetchErr = a.fetchPrometheusRangeWindow(ctx, "histogram_quantile(0.99, sum by (le) (rate(gateway_request_duration_seconds_bucket[1m])))", start, end, dashboardRangeStep)
			return fetchErr
		},
		func(ctx context.Context) error {
			var fetchErr error
			circuitState, fetchErr = a.fetchPrometheusRangeWindow(ctx, "gateway_circuit_state{service=~\".+\"}", start, end, dashboardRangeStep)
			return fetchErr
		},
		func(ctx context.Context) error {
			var fetchErr error
			rejectedRate, fetchErr = a.fetchPrometheusRangeWindow(ctx, "sum(rate(gateway_rate_limit_rejections_total[1m]))", start, end, dashboardRangeStep)
			return fetchErr
		},
		func(ctx context.Context) error {
			var fetchErr error
			successCount, fetchErr = a.fetchPrometheusInstantAt(ctx, "sum(increase(gateway_requests_total[1m]))", end)
			return fetchErr
		},
		func(ctx context.Context) error {
			var fetchErr error
			errorCount, fetchErr = a.fetchPrometheusInstantAt(ctx, "sum(increase(gateway_request_failures_total[1m]))", end)
			return fetchErr
		},
		func(ctx context.Context) error {
			var fetchErr error
			retryCount, fetchErr = a.fetchPrometheusInstantAt(ctx, "sum(increase(gateway_retries_total[1m]))", end)
			return fetchErr
		},
		func(ctx context.Context) error {
			var fetchErr error
			rateLimitedCount, fetchErr = a.fetchPrometheusInstantAt(ctx, "sum(increase(gateway_rate_limit_rejections_total[1m]))", end)
			return fetchErr
		},
	)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}

	totalRate := requestRate

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
	return a.fetchPrometheusInstantAt(ctx, query, time.Now().UTC())
}

func (a *app) fetchPrometheusInstantAt(ctx context.Context, query string, at time.Time) (json.RawMessage, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("time", fmt.Sprintf("%d", at.Unix()))
	return a.fetchPrometheusJSON(ctx, "http://prometheus:9090/api/v1/query", params)
}

func (a *app) fetchPrometheusRange(ctx context.Context, query string, window time.Duration, step time.Duration) (json.RawMessage, error) {
	now := time.Now().UTC()
	return a.fetchPrometheusRangeWindow(ctx, query, now.Add(-window), now, step)
}

func (a *app) fetchPrometheusRangeWindow(
	ctx context.Context,
	query string,
	start time.Time,
	end time.Time,
	step time.Duration,
) (json.RawMessage, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", fmt.Sprintf("%d", start.Unix()))
	params.Set("end", fmt.Sprintf("%d", end.Unix()))
	params.Set("step", fmt.Sprintf("%ds", int(step/time.Second)))
	return a.fetchPrometheusJSON(ctx, "http://prometheus:9090/api/v1/query_range", params)
}

func runDashboardTasks(parent context.Context, tasks ...func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(parent, dashboardQueryTimeout)
	defer cancel()

	var (
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
	)

	for _, task := range tasks {
		task := task
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := task(ctx); err != nil {
				once.Do(func() {
					firstErr = err
					cancel()
				})
			}
		}()
	}

	wg.Wait()
	return firstErr
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
