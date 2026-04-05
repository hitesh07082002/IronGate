package transport

import (
	"context"
	"net/http"
	"testing"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/hitesh07082002/irongate/internal/middleware"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestLoadBalancerTransportInitializesNilResponseHeaders(t *testing.T) {
	transport := NewResilientTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
			Request:    req,
		}, nil
	}))

	route := &config.RouteConfig{
		Path:         "/api/users",
		Service:      "user-service",
		LoadBalancer: "round_robin",
		Targets: []config.Target{
			{Host: "user-service-1", Port: 8081},
		},
	}

	req, err := http.NewRequest(http.MethodGet, "http://gateway/api/users", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req = req.WithContext(context.WithValue(req.Context(), middleware.RouteConfigKey, route))

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if resp.Header == nil {
		t.Fatal("expected transport to initialize response headers")
	}
	if got := resp.Header.Get(servedByHeader); got != "user-service-1:8081" {
		t.Fatalf("expected %q, got %q", "user-service-1:8081", got)
	}
}
