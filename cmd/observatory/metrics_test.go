package main

import (
	"fmt"
	"sync"
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
	if !allowedPrometheusQuery("histogram_quantile(0.95, sum by (le) (rate(gateway_request_duration_seconds_bucket[1m])))") {
		t.Fatal("expected allowlisted aggregation query to pass")
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

func TestIPRateLimiterConcurrent(t *testing.T) {
	limiter := NewIPRateLimiter(1000, time.Minute)
	base := time.Now()

	var wg sync.WaitGroup
	for index := 0; index < 100; index++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			key := fmt.Sprintf("192.168.1.%d", worker%16)
			for step := 0; step < 100; step++ {
				_ = limiter.Allow(key, base.Add(time.Duration(step)*time.Millisecond))
			}
		}(index)
	}

	wg.Wait()
}
