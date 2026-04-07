package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var allowedQueryPrefixes = []string{
	"gateway_requests_total",
	"gateway_request_duration_seconds",
	"gateway_request_failures_total",
	"gateway_rate_limit_rejections_total",
	"gateway_retries_total",
	"gateway_retry_delay_seconds",
	"gateway_circuit_opens_total",
	"gateway_open_circuits",
	"gateway_circuit_state",
	"gateway_in_flight_requests",
	"gateway_upstream_duration_seconds",
	"rate(",
	"histogram_quantile(",
	"increase(",
	"sum(",
	"avg(",
}

func (a *app) handleMetricsQuery(w http.ResponseWriter, r *http.Request) {
	a.handlePrometheusProxy(w, r, "http://prometheus:9090/api/v1/query")
}

func (a *app) handleMetricsQueryRange(w http.ResponseWriter, r *http.Request) {
	a.handlePrometheusProxy(w, r, "http://prometheus:9090/api/v1/query_range")
}

func (a *app) handlePrometheusProxy(w http.ResponseWriter, r *http.Request, upstreamBase string) {
	if !a.metricsLimiter.Allow(clientIPFromRequest(r), time.Now()) {
		writeAPIError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("query"))
	if !allowedPrometheusQuery(query) {
		writeAPIError(w, http.StatusForbidden, "query not permitted")
		return
	}

	upstreamURL := upstreamBase
	if rawQuery := strings.TrimSpace(r.URL.RawQuery); rawQuery != "" {
		upstreamURL += "?" + rawQuery
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstreamURL, nil)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, fmt.Sprintf("build prometheus request: %v", err))
		return
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, fmt.Sprintf("query prometheus: %v", err))
		return
	}
	defer resp.Body.Close()

	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func allowedPrometheusQuery(query string) bool {
	trimmed := strings.TrimSpace(query)
	for _, prefix := range allowedQueryPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}

	return false
}
