package main

import (
	"testing"
	"time"
)

func TestAllowedPrometheusQuery(t *testing.T) {
	if !allowedPrometheusQuery("gateway_circuit_state") {
		t.Fatal("expected direct allowlisted metric to pass")
	}
	if !allowedPrometheusQuery("rate(gateway_requests_total[1m])") {
		t.Fatal("expected allowlisted function prefix to pass")
	}
	if allowedPrometheusQuery("up{job='evil'}") {
		t.Fatal("expected non-allowlisted query to fail")
	}
}

func TestIPRateLimiterRejectsAfterLimit(t *testing.T) {
	limiter := NewIPRateLimiter(2, time.Minute)
	now := time.Unix(100, 0)

	if !limiter.Allow("127.0.0.1", now) {
		t.Fatal("expected first request to pass")
	}
	if !limiter.Allow("127.0.0.1", now) {
		t.Fatal("expected second request to pass")
	}
	if limiter.Allow("127.0.0.1", now) {
		t.Fatal("expected third request to be rejected")
	}
}
