package transport

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/hitesh07082002/irongate/internal/middleware"
	"github.com/hitesh07082002/irongate/internal/transport/loadbalancer"
)

const servedByHeader = "X-Served-By"

type LoadBalancerTransport struct {
	next     http.RoundTripper
	registry *balancerRegistry
}

func NewResilientTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = NewBaseTransport()
	}

	return &LoadBalancerTransport{
		next:     base,
		registry: &balancerRegistry{},
	}
}

func NewBaseTransport() http.RoundTripper {
	return &http.Transport{
		MaxIdleConnsPerHost: 100,
		MaxConnsPerHost:     100,
		IdleConnTimeout:     90 * time.Second,
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

	selection, err := balancer.Select()
	if err != nil {
		return nil, err
	}

	outbound := req.Clone(req.Context())
	outbound.URL.Scheme = "http"
	outbound.URL.Host = net.JoinHostPort(selection.Target.Host, strconv.Itoa(selection.Target.Port))
	outbound.Host = outbound.URL.Host

	resp, err := lt.next.RoundTrip(outbound)
	if err != nil {
		selection.Done()
		return nil, err
	}
	if resp == nil {
		selection.Done()
		return nil, fmt.Errorf("upstream transport returned nil response")
	}

	resp.Header.Set(servedByHeader, outbound.URL.Host)
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
