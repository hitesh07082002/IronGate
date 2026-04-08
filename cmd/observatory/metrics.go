package main

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var allowedMetricNames = []string{
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
}

var promQLTokenPattern = regexp.MustCompile(`[A-Za-z_:][A-Za-z0-9_:]*`)

var allowedPromQLFunctions = map[string]struct{}{
	"avg":                {},
	"clamp_min":          {},
	"histogram_quantile": {},
	"increase":           {},
	"max":                {},
	"rate":               {},
	"sum":                {},
}

var allowedPromQLKeywords = map[string]struct{}{
	"bool":            {},
	"by":              {},
	"group_left":      {},
	"group_right":     {},
	"ignoring":        {},
	"le":              {},
	"offset":          {},
	"on":              {},
	"service":         {},
	"without":         {},
	"__rate_interval": {},
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
	if trimmed == "" {
		return false
	}

	indices := promQLTokenPattern.FindAllStringIndex(trimmed, -1)
	if len(indices) == 0 {
		return false
	}

	hasAllowedMetric := false
	for _, index := range indices {
		token := trimmed[index[0]:index[1]]
		lower := strings.ToLower(token)

		if allowedMetricIdentifier(token) {
			hasAllowedMetric = true
			continue
		}
		if _, ok := allowedPromQLKeywords[lower]; ok {
			continue
		}
		if isPromQLDurationUnit(trimmed, index[0], lower) {
			continue
		}
		if _, ok := allowedPromQLFunctions[lower]; ok {
			continue
		}

		return false
	}

	return hasAllowedMetric
}

func allowedMetricIdentifier(token string) bool {
	for _, metric := range allowedMetricNames {
		if token == metric || strings.HasPrefix(token, metric+"_") {
			return true
		}
	}

	return false
}

func isPromQLDurationUnit(query string, tokenStart int, token string) bool {
	if tokenStart == 0 || len(token) != 1 {
		return false
	}

	switch token {
	case "s", "m", "h", "d", "w", "y":
	default:
		return false
	}

	return query[tokenStart-1] >= '0' && query[tokenStart-1] <= '9'
}
