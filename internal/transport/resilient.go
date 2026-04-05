package transport

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/hitesh07082002/irongate/internal/middleware"
	"github.com/hitesh07082002/irongate/internal/transport/circuitbreaker"
	"github.com/hitesh07082002/irongate/internal/transport/loadbalancer"
)

type LoadBalancerTransport struct {
	next     http.RoundTripper
	registry *balancerRegistry
}

type CircuitBreakerTransport struct {
	next     http.RoundTripper
	registry *circuitbreaker.Registry
}

func NewResilientTransport(base http.RoundTripper, breakerConfig config.CBConfig) http.RoundTripper {
	if base == nil {
		base = NewBaseTransport()
	}

	breakers := &CircuitBreakerTransport{
		next:     base,
		registry: circuitbreaker.NewRegistry(breakerConfig),
	}
	loadBalancing := &LoadBalancerTransport{
		next:     breakers,
		registry: &balancerRegistry{},
	}

	return NewRetryTransport(loadBalancing)
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
	route := middleware.GetRouteConfig(req)
	if route == nil {
		return nil, fmt.Errorf("route config missing from request context")
	}
	if len(route.Targets) == 0 {
		return nil, loadbalancer.ErrNoTargets
	}

	balancer, err := lt.registry.balancerFor(route)
	if err != nil {
		return nil, err
	}

	selection, err := balancer.Select(loadbalancer.SelectionOptions{
		ExcludeTargets: getAttemptMetadata(req).excludedTargets(),
	})
	if err != nil {
		return nil, err
	}

	outbound := req.Clone(req.Context())
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
	target := getAttemptMetadata(req).target
	if target == "" {
		target = req.URL.Host
	}
	if target == "" {
		return nil, fmt.Errorf("upstream target missing from request")
	}

	breaker := ct.registry.Breaker(target)
	if !breaker.Allow() {
		return nil, ErrCircuitOpen
	}

	resp, err := ct.next.RoundTrip(req)
	if err != nil {
		if countsTowardCircuit(err) {
			breaker.RecordFailure()
		} else {
			breaker.RecordIgnored()
		}
		return nil, err
	}
	if resp == nil {
		breaker.RecordFailure()
		return nil, fmt.Errorf("upstream transport returned nil response")
	}

	if resp.StatusCode >= http.StatusInternalServerError {
		breaker.RecordFailure()
	} else {
		breaker.RecordSuccess()
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

func countsTowardCircuit(err error) bool {
	return isTransientError(err)
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
