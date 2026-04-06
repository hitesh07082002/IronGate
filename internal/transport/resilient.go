package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/hitesh07082002/irongate/internal/config"
	gatewaymetrics "github.com/hitesh07082002/irongate/internal/metrics"
	"github.com/hitesh07082002/irongate/internal/middleware"
	"github.com/hitesh07082002/irongate/internal/transport/circuitbreaker"
	"github.com/hitesh07082002/irongate/internal/transport/loadbalancer"
)

type LoadBalancerTransport struct {
	next     http.RoundTripper
	registry *balancerRegistry
	tracer   trace.Tracer
}

type CircuitBreakerTransport struct {
	next           http.RoundTripper
	registry       *circuitbreaker.Registry
	metrics        *gatewaymetrics.Registry
	serviceTargets map[string][]config.Target
	tracer         trace.Tracer
}

type UpstreamTransport struct {
	next   http.RoundTripper
	tracer trace.Tracer
}

func NewResilientTransport(base http.RoundTripper, routes []config.RouteConfig, breakerConfig config.CBConfig, registry *gatewaymetrics.Registry, breakers *circuitbreaker.Registry, tracer trace.Tracer) http.RoundTripper {
	if base == nil {
		base = NewBaseTransport()
	}
	if breakers == nil {
		if registry != nil {
			breakers = circuitbreaker.NewRegistry(breakerConfig, registry.RegisterCollector)
		} else {
			breakers = circuitbreaker.NewRegistry(breakerConfig, nil)
		}
	}

	upstreamTransport := &UpstreamTransport{
		next:   base,
		tracer: tracer,
	}
	breakerTransport := &CircuitBreakerTransport{
		next:           upstreamTransport,
		registry:       breakers,
		metrics:        registry,
		serviceTargets: serviceTargetSets(routes),
		tracer:         tracer,
	}
	loadBalancing := &LoadBalancerTransport{
		next:     breakerTransport,
		registry: &balancerRegistry{},
		tracer:   tracer,
	}

	return NewRetryTransport(loadBalancing, registry, tracer)
}

func NewBaseTransport() http.RoundTripper {
	return &http.Transport{
		MaxIdleConnsPerHost:   100,
		MaxConnsPerHost:       100,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
}

func (lt *LoadBalancerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, span := transportTracerOrNoop(lt.tracer, "irongate.transport").Start(req.Context(), "irongate.transport.loadbalancer")
	defer span.End()
	req = req.Clone(ctx)

	route := middleware.GetRouteConfig(req)
	if route == nil {
		span.SetStatus(codes.Error, "route config missing from request context")
		return nil, fmt.Errorf("route config missing from request context")
	}
	if len(route.Targets) == 0 {
		span.SetStatus(codes.Error, loadbalancer.ErrNoTargets.Error())
		return nil, loadbalancer.ErrNoTargets
	}

	strategy := route.LoadBalancer
	if strings.TrimSpace(strategy) == "" {
		strategy = "round_robin"
	}
	balancer, err := lt.registry.balancerFor(route)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	selection, err := balancer.Select(loadbalancer.SelectionOptions{
		ExcludeTargets: getAttemptMetadata(req).excludedTargets(),
	})
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	span.SetAttributes(
		attribute.String("lb.strategy", strategy),
		attribute.String("lb.selected", targetAddress(selection.Target)),
	)

	outbound := req.Clone(ctx)
	outbound.URL.Scheme = "http"
	outbound.URL.Host = targetAddress(selection.Target)
	outbound.Host = outbound.URL.Host
	metadata := getAttemptMetadata(outbound).withTarget(outbound.URL.Host)
	outbound = withAttemptMetadata(outbound, metadata)

	resp, err := lt.next.RoundTrip(outbound)
	if err != nil {
		selection.Done()
		return nil, wrapAttemptError(err, metadata)
	}
	if resp == nil {
		selection.Done()
		return nil, wrapAttemptError(fmt.Errorf("upstream transport returned nil response"), metadata)
	}
	if resp.Header == nil {
		resp.Header = make(http.Header)
	}

	resp.Header.Set(HeaderServedBy, outbound.URL.Host)
	applyAttemptHeaders(resp.Header, metadata)
	if resp.Request == nil {
		resp.Request = outbound
	}

	if resp.Body == nil {
		selection.Done()
		resp.Body = http.NoBody
		return resp, nil
	}

	resp.Body = &releaseOnReadCloser{
		ReadCloser: resp.Body,
		release:    selection.Done,
	}

	return resp, nil
}

func (ct *CircuitBreakerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, span := transportTracerOrNoop(ct.tracer, "irongate.transport").Start(req.Context(), "irongate.transport.circuitbreaker")
	defer span.End()
	req = req.Clone(ctx)

	service := routeService(req)
	routeTargets := ct.targetsForService(service, routeTargets(req))
	target := getAttemptMetadata(req).target
	if target == "" {
		target = req.URL.Host
	}
	if target == "" {
		span.SetStatus(codes.Error, "upstream target missing from request")
		return nil, fmt.Errorf("upstream target missing from request")
	}

	breaker := ct.registry.BreakerForService(target, service)
	allowed := breaker.Allow()
	span.SetAttributes(
		attribute.String("cb.target", target),
		attribute.String("cb.state", circuitStateAttribute(breaker.State())),
	)
	ct.syncOpenCircuitGauge(service, routeTargets, target)
	if !allowed {
		span.AddEvent("circuit_rejected")
		span.SetStatus(codes.Error, ErrCircuitOpen.Error())
		return nil, ErrCircuitOpen
	}

	start := time.Now()
	resp, err := ct.next.RoundTrip(req)
	ct.metrics.ObserveUpstreamDuration(service, time.Since(start))
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		if countsTowardCircuit(req.Context(), err) {
			ct.recordBreakerFailure(breaker, service, routeTargets, target)
		} else {
			ct.recordBreakerIgnored(breaker, service, routeTargets, target)
		}
		return nil, err
	}
	if resp == nil {
		span.SetStatus(codes.Error, "upstream transport returned nil response")
		ct.recordBreakerFailure(breaker, service, routeTargets, target)
		return nil, fmt.Errorf("upstream transport returned nil response")
	}

	if resp.StatusCode >= http.StatusInternalServerError {
		span.SetStatus(codes.Error, http.StatusText(resp.StatusCode))
		ct.recordBreakerFailure(breaker, service, routeTargets, target)
		return resp, nil
	}
	if resp.Body == nil {
		ct.recordBreakerSuccess(breaker, service, routeTargets, target)
		resp.Body = http.NoBody
		return resp, nil
	}

	resp.Body = &breakerOnReadCloser{
		ReadCloser: resp.Body,
		ctx:        req.Context(),
		succeed: func() {
			ct.recordBreakerSuccess(breaker, service, routeTargets, target)
		},
		fail: func() {
			ct.recordBreakerFailure(breaker, service, routeTargets, target)
		},
		ignore: func() {
			ct.recordBreakerIgnored(breaker, service, routeTargets, target)
		},
	}

	return resp, nil
}

func (ut *UpstreamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, span := transportTracerOrNoop(ut.tracer, "irongate.transport").Start(req.Context(), "irongate.transport.upstream")
	defer span.End()

	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req = req.Clone(ctx)
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))

	target := req.URL.Host
	start := time.Now()
	resp, err := ut.next.RoundTrip(req)
	durationMs := float64(time.Since(start).Milliseconds())
	span.SetAttributes(
		attribute.String("upstream.target", target),
		attribute.Float64("upstream.duration_ms", durationMs),
	)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	if resp != nil {
		span.SetAttributes(attribute.Int("upstream.status", resp.StatusCode))
		if resp.StatusCode >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, http.StatusText(resp.StatusCode))
		}
		if resp.Request == nil {
			resp.Request = req
		}
	}

	return resp, nil
}

type balancerRegistry struct {
	balancers sync.Map
}

func (r *balancerRegistry) balancerFor(route *config.RouteConfig) (loadbalancer.Balancer, error) {
	key := routeBalancerKey(route)
	if balancer, ok := r.balancers.Load(key); ok {
		return balancer.(loadbalancer.Balancer), nil
	}

	balancer, err := loadbalancer.New(route.LoadBalancer, route.Targets)
	if err != nil {
		return nil, err
	}

	actual, _ := r.balancers.LoadOrStore(key, balancer)
	return actual.(loadbalancer.Balancer), nil
}

func routeBalancerKey(route *config.RouteConfig) string {
	var builder strings.Builder

	builder.WriteString(route.Service)
	builder.WriteByte('|')
	builder.WriteString(route.LoadBalancer)
	for _, target := range route.Targets {
		builder.WriteByte('|')
		builder.WriteString(target.Host)
		builder.WriteByte(':')
		builder.WriteString(strconv.Itoa(target.Port))
		builder.WriteByte('@')
		builder.WriteString(strconv.Itoa(target.Weight))
	}

	return builder.String()
}

func wrapAttemptError(err error, metadata attemptMetadata) error {
	if err == nil {
		return nil
	}

	var attemptErr *AttemptError
	if errors.As(err, &attemptErr) {
		return err
	}

	return &AttemptError{
		Err:        err,
		RetryCount: metadata.retryCount,
		Target:     metadata.target,
	}
}

func countsTowardCircuit(ctx context.Context, err error) bool {
	if isCallerContextError(ctx, err) {
		return false
	}

	return isTransientError(err)
}

func (ct *CircuitBreakerTransport) recordBreakerFailure(breaker *circuitbreaker.Breaker, service string, targets []config.Target, fallbackTarget string) {
	before := breaker.State()
	breaker.RecordFailure()
	after := breaker.State()

	if before != circuitbreaker.StateOpen && after == circuitbreaker.StateOpen {
		ct.metrics.IncCircuitOpen(service)
	}
	ct.syncOpenCircuitGauge(service, targets, fallbackTarget)
}

func (ct *CircuitBreakerTransport) recordBreakerSuccess(breaker *circuitbreaker.Breaker, service string, targets []config.Target, fallbackTarget string) {
	breaker.RecordSuccess()
	ct.syncOpenCircuitGauge(service, targets, fallbackTarget)
}

func (ct *CircuitBreakerTransport) recordBreakerIgnored(breaker *circuitbreaker.Breaker, service string, targets []config.Target, fallbackTarget string) {
	breaker.RecordIgnored()
	ct.syncOpenCircuitGauge(service, targets, fallbackTarget)
}

func (ct *CircuitBreakerTransport) syncOpenCircuitGauge(service string, targets []config.Target, fallbackTarget string) {
	if ct.metrics == nil {
		return
	}

	ct.metrics.SetOpenCircuits(service, ct.openCircuitCount(targets, fallbackTarget))
}

func (ct *CircuitBreakerTransport) openCircuitCount(targets []config.Target, fallbackTarget string) int {
	if len(targets) == 0 {
		if fallbackTarget == "" {
			return 0
		}
		if ct.registry.Breaker(fallbackTarget).State() == circuitbreaker.StateOpen {
			return 1
		}
		return 0
	}

	seen := make(map[string]struct{}, len(targets))
	openCount := 0
	for _, target := range targets {
		address := targetAddress(target)
		if _, ok := seen[address]; ok {
			continue
		}
		seen[address] = struct{}{}
		if ct.registry.Breaker(address).State() == circuitbreaker.StateOpen {
			openCount++
		}
	}

	return openCount
}

func (ct *CircuitBreakerTransport) targetsForService(service string, fallback []config.Target) []config.Target {
	if len(ct.serviceTargets) == 0 || service == "" {
		return fallback
	}

	targets, ok := ct.serviceTargets[service]
	if !ok || len(targets) == 0 {
		return fallback
	}

	return targets
}

func isCallerContextError(ctx context.Context, err error) bool {
	switch {
	case errors.Is(err, context.Canceled):
		return ctx == nil || errors.Is(ctx.Err(), context.Canceled)
	case errors.Is(err, context.DeadlineExceeded):
		return ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)
	default:
		return false
	}
}

func routeService(req *http.Request) string {
	if req == nil {
		return ""
	}

	if route := middleware.GetRouteConfig(req); route != nil {
		return route.Service
	}

	return ""
}

func routeTargets(req *http.Request) []config.Target {
	if req == nil {
		return nil
	}

	if route := middleware.GetRouteConfig(req); route != nil {
		return route.Targets
	}

	return nil
}

func serviceTargetSets(routes []config.RouteConfig) map[string][]config.Target {
	serviceTargets := make(map[string][]config.Target)
	seen := make(map[string]map[string]struct{})

	for _, route := range routes {
		if route.Service == "" || len(route.Targets) == 0 {
			continue
		}
		if _, ok := seen[route.Service]; !ok {
			seen[route.Service] = make(map[string]struct{})
		}
		for _, target := range route.Targets {
			address := targetAddress(target)
			if _, ok := seen[route.Service][address]; ok {
				continue
			}
			seen[route.Service][address] = struct{}{}
			serviceTargets[route.Service] = append(serviceTargets[route.Service], target)
		}
	}

	return serviceTargets
}

func circuitStateAttribute(state circuitbreaker.State) string {
	switch state {
	case circuitbreaker.StateOpen:
		return "open"
	case circuitbreaker.StateHalfOpen:
		return "half_open"
	default:
		return "closed"
	}
}

func transportTracerOrNoop(tracer trace.Tracer, name string) trace.Tracer {
	if tracer == nil {
		return noop.NewTracerProvider().Tracer(name)
	}

	return tracer
}

type releaseOnReadCloser struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (r *releaseOnReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if err == io.EOF {
		r.once.Do(r.release)
	}

	return n, err
}

func (r *releaseOnReadCloser) Close() error {
	err := r.ReadCloser.Close()
	r.once.Do(r.release)
	return err
}

type breakerOnReadCloser struct {
	io.ReadCloser
	once    sync.Once
	ctx     context.Context
	succeed func()
	fail    func()
	ignore  func()
}

func (r *breakerOnReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	switch {
	case err == nil:
		return n, nil
	case errors.Is(err, io.EOF):
		r.once.Do(r.succeed)
	case isCallerContextError(r.ctx, err):
		r.once.Do(r.ignore)
	default:
		r.once.Do(r.fail)
	}

	return n, err
}

func (r *breakerOnReadCloser) Close() error {
	err := r.ReadCloser.Close()
	if err != nil {
		if isCallerContextError(r.ctx, err) {
			r.once.Do(r.ignore)
		} else {
			r.once.Do(r.fail)
		}
		return err
	}

	r.once.Do(r.ignore)
	return nil
}
