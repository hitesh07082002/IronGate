package metrics

import (
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
)

const (
	serviceLabel   = "service"
	unknownService = "unknown"

	MetricRequestsTotal        = "gateway_requests_total"
	MetricRequestFailuresTotal = "gateway_request_failures_total"
	MetricRequestDuration      = "gateway_request_duration_seconds"
	MetricRateLimitRejections  = "gateway_rate_limit_rejections_total"
	MetricRetriesTotal         = "gateway_retries_total"
	MetricRetryDelay           = "gateway_retry_delay_seconds"
	MetricCircuitOpensTotal    = "gateway_circuit_opens_total"
	MetricOpenCircuits         = "gateway_open_circuits"
	MetricUpstreamDuration     = "gateway_upstream_duration_seconds"
	MetricInFlightRequests     = "gateway_in_flight_requests"
)

var (
	requestDurationBuckets  = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
	retryDelayBuckets       = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5}
	upstreamDurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
)

type counter struct {
	vec *prometheus.CounterVec
}

func newCounter(name, help string) counter {
	return counter{
		vec: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: name,
				Help: help,
			},
			[]string{serviceLabel},
		),
	}
}

func (c counter) collector() prometheus.Collector {
	return c.vec
}

func (c counter) Inc(service string) {
	if c.vec == nil {
		return
	}

	c.vec.WithLabelValues(normalizeService(service)).Inc()
}

type gauge struct {
	vec *prometheus.GaugeVec
}

func newGauge(name, help string) gauge {
	return gauge{
		vec: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: name,
				Help: help,
			},
			[]string{serviceLabel},
		),
	}
}

func (g gauge) collector() prometheus.Collector {
	return g.vec
}

func (g gauge) Inc(service string) {
	if g.vec == nil {
		return
	}

	g.vec.WithLabelValues(normalizeService(service)).Inc()
}

func (g gauge) Dec(service string) {
	if g.vec == nil {
		return
	}

	g.vec.WithLabelValues(normalizeService(service)).Dec()
}

func (g gauge) Set(service string, value float64) {
	if g.vec == nil {
		return
	}

	g.vec.WithLabelValues(normalizeService(service)).Set(value)
}

type histogram struct {
	vec *prometheus.HistogramVec
}

func newHistogram(name, help string, buckets []float64) histogram {
	return histogram{
		vec: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    name,
				Help:    help,
				Buckets: buckets,
			},
			[]string{serviceLabel},
		),
	}
}

func (h histogram) collector() prometheus.Collector {
	return h.vec
}

func (h histogram) Observe(service string, value float64) {
	if h.vec == nil {
		return
	}

	h.vec.WithLabelValues(normalizeService(service)).Observe(value)
}

func (h histogram) ObserveDuration(service string, duration time.Duration) {
	h.Observe(service, duration.Seconds())
}

type Registry struct {
	registry *prometheus.Registry

	requestsTotal        counter
	requestFailuresTotal counter
	requestDuration      histogram
	rateLimitRejections  counter
	retriesTotal         counter
	retryDelay           histogram
	circuitOpensTotal    counter
	openCircuits         gauge
	upstreamDuration     histogram
	inFlightRequests     gauge
}

func NewRegistry() *Registry {
	registry := prometheus.NewRegistry()
	metrics := &Registry{
		registry: registry,
		requestsTotal: newCounter(
			MetricRequestsTotal,
			"Total gateway requests completed per service.",
		),
		requestFailuresTotal: newCounter(
			MetricRequestFailuresTotal,
			"Total gateway requests that completed with a 5xx status per service.",
		),
		requestDuration: newHistogram(
			MetricRequestDuration,
			"End-to-end gateway request duration in seconds per service.",
			requestDurationBuckets,
		),
		rateLimitRejections: newCounter(
			MetricRateLimitRejections,
			"Total requests rejected by rate limiting per service.",
		),
		retriesTotal: newCounter(
			MetricRetriesTotal,
			"Total retry attempts executed beyond the first upstream attempt per service.",
		),
		retryDelay: newHistogram(
			MetricRetryDelay,
			"Backoff delay before retry attempts in seconds per service.",
			retryDelayBuckets,
		),
		circuitOpensTotal: newCounter(
			MetricCircuitOpensTotal,
			"Total circuit breaker open transitions per service.",
		),
		openCircuits: newGauge(
			MetricOpenCircuits,
			"Current number of open circuit breakers per service.",
		),
		upstreamDuration: newHistogram(
			MetricUpstreamDuration,
			"Upstream round-trip duration in seconds per service.",
			upstreamDurationBuckets,
		),
		inFlightRequests: newGauge(
			MetricInFlightRequests,
			"Current number of in-flight gateway requests per service.",
		),
	}

	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		metrics.requestsTotal.collector(),
		metrics.requestFailuresTotal.collector(),
		metrics.requestDuration.collector(),
		metrics.rateLimitRejections.collector(),
		metrics.retriesTotal.collector(),
		metrics.retryDelay.collector(),
		metrics.circuitOpensTotal.collector(),
		metrics.openCircuits.collector(),
		metrics.upstreamDuration.collector(),
		metrics.inFlightRequests.collector(),
	)

	return metrics
}

func (r *Registry) Handler() http.Handler {
	if r == nil || r.registry == nil {
		return http.NotFoundHandler()
	}

	return promhttp.HandlerFor(r.registry, promhttp.HandlerOpts{})
}

func (r *Registry) Gather() ([]*dto.MetricFamily, error) {
	if r == nil || r.registry == nil {
		return nil, nil
	}

	return r.registry.Gather()
}

func (r *Registry) ObserveRequest(service string, statusCode int, duration time.Duration) {
	if r == nil {
		return
	}

	r.requestsTotal.Inc(service)
	r.requestDuration.ObserveDuration(service, duration)
	if statusCode >= http.StatusInternalServerError {
		r.requestFailuresTotal.Inc(service)
	}
}

func (r *Registry) IncRateLimitRejection(service string) {
	if r == nil {
		return
	}

	r.rateLimitRejections.Inc(service)
}

func (r *Registry) IncRetry(service string) {
	if r == nil {
		return
	}

	r.retriesTotal.Inc(service)
}

func (r *Registry) ObserveRetryDelay(service string, delay time.Duration) {
	if r == nil {
		return
	}

	r.retryDelay.ObserveDuration(service, delay)
}

func (r *Registry) IncCircuitOpen(service string) {
	if r == nil {
		return
	}

	r.circuitOpensTotal.Inc(service)
}

func (r *Registry) SetOpenCircuits(service string, count int) {
	if r == nil {
		return
	}

	r.openCircuits.Set(service, float64(count))
}

func (r *Registry) ObserveUpstreamDuration(service string, duration time.Duration) {
	if r == nil {
		return
	}

	r.upstreamDuration.ObserveDuration(service, duration)
}

func (r *Registry) IncInFlight(service string) {
	if r == nil {
		return
	}

	r.inFlightRequests.Inc(service)
}

func (r *Registry) DecInFlight(service string) {
	if r == nil {
		return
	}

	r.inFlightRequests.Dec(service)
}

func normalizeService(service string) string {
	service = strings.TrimSpace(service)
	if service == "" {
		return unknownService
	}

	return service
}
