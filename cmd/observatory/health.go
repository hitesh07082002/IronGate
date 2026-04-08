package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	serviceStatusHealthy = "healthy"
	serviceStatusDown    = "down"
	serviceStatusTimeout = "timeout"
	serviceStatusUnknown = "unknown"
)

type serviceHealth struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CheckedAt time.Time `json:"checked_at,omitempty"`
	Details   string    `json:"details,omitempty"`
}

type monitoredService struct {
	Name string
	URL  string
	Kind string
}

var observatoryHealthServices = []monitoredService{
	{Name: "gateway", URL: "http://gateway:8080/health", Kind: "http"},
	{Name: "redis", Kind: "redis"},
	{Name: "user-service-1", URL: "http://user-service-1:8081/health", Kind: "http"},
	{Name: "user-service-2", URL: "http://user-service-2:8091/health", Kind: "http"},
	{Name: "order-service-1", URL: "http://order-service-1:8082/health", Kind: "http"},
	{Name: "order-service-2", URL: "http://order-service-2:8092/health", Kind: "http"},
}

func (a *app) healthSnapshot(ctx context.Context) healthResponse {
	if ctx == nil {
		ctx = context.Background()
	}

	return healthResponse{
		Status:         "ok",
		SpecVersion:    observatorySpecVersion,
		JWTValid:       strings.TrimSpace(a.currentDemoJWT()) != "",
		ToxiproxyReady: a.toxiproxyReady,
		Services:       a.collectServiceHealth(ctx),
	}
}

func (a *app) collectServiceHealth(ctx context.Context) []serviceHealth {
	results := make([]serviceHealth, len(observatoryHealthServices))
	done := make(chan struct {
		index  int
		health serviceHealth
	}, len(observatoryHealthServices))

	for index, service := range observatoryHealthServices {
		go func(index int, service monitoredService) {
			done <- struct {
				index  int
				health serviceHealth
			}{
				index:  index,
				health: a.checkServiceHealth(ctx, service),
			}
		}(index, service)
	}

	for range observatoryHealthServices {
		result := <-done
		results[result.index] = result.health
	}

	return results
}

func (a *app) checkServiceHealth(ctx context.Context, service monitoredService) serviceHealth {
	probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	checkedAt := time.Now().UTC()
	switch service.Kind {
	case "redis":
		if a.redis == nil {
			return serviceHealth{Name: service.Name, Status: serviceStatusUnknown, CheckedAt: checkedAt}
		}
		if err := a.redis.Ping(probeCtx); err != nil {
			return serviceHealth{Name: service.Name, Status: classifyHealthError(err, probeCtx), CheckedAt: checkedAt, Details: err.Error()}
		}
		return serviceHealth{Name: service.Name, Status: serviceStatusHealthy, CheckedAt: checkedAt}
	default:
		req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, service.URL, nil)
		if err != nil {
			return serviceHealth{Name: service.Name, Status: serviceStatusDown, CheckedAt: checkedAt, Details: err.Error()}
		}

		resp, err := a.httpClient.Do(req)
		if err != nil {
			return serviceHealth{Name: service.Name, Status: classifyHealthError(err, probeCtx), CheckedAt: checkedAt, Details: err.Error()}
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return serviceHealth{
				Name:      service.Name,
				Status:    serviceStatusDown,
				CheckedAt: checkedAt,
				Details:   fmt.Sprintf("returned %d", resp.StatusCode),
			}
		}

		return serviceHealth{Name: service.Name, Status: serviceStatusHealthy, CheckedAt: checkedAt}
	}
}

func classifyHealthError(err error, ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return serviceStatusTimeout
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return serviceStatusTimeout
	}

	return serviceStatusDown
}
