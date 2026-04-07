package main

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

var resetDrainDelay = 5 * time.Second

type resetResponse struct {
	Status          string `json:"status"`
	ServicesHealthy bool   `json:"services_healthy,omitempty"`
	FailedStep      string `json:"failed_step,omitempty"`
	Details         string `json:"details,omitempty"`
}

func (a *app) handleReset(w http.ResponseWriter, r *http.Request) {
	response := a.resetSystem(r.Context())
	writeJSON(w, http.StatusOK, response)
}

func (a *app) resetSystem(ctx context.Context) resetResponse {
	resetCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	a.cancelActiveScenario()
	if err := a.runner.StopManagedContainers(resetCtx); err != nil {
		return partialReset("stop_k6_containers", err)
	}
	if err := a.resetChaos(resetCtx); err != nil {
		return partialReset("reset_chaos", err)
	}
	if err := a.resetCircuitBreakers(resetCtx); err != nil {
		return partialReset("reset_circuit_breakers", err)
	}
	if err := a.toxiproxy.RemoveAllToxics(resetCtx); err != nil {
		return partialReset("remove_toxics", err)
	}
	if err := a.flushRedisRateLimitKeys(resetCtx); err != nil {
		return partialReset("flush_redis", err)
	}

	select {
	case <-resetCtx.Done():
		return partialReset("drain_wait", resetCtx.Err())
	case <-time.After(resetDrainDelay):
	}

	if err := a.verifyServiceHealth(resetCtx); err != nil {
		return partialReset("verify_services", err)
	}

	a.eventHub.Publish(SystemEvent("reset_complete", "reset completed", map[string]any{
		"services_healthy": true,
	}))

	return resetResponse{
		Status:          "clean",
		ServicesHealthy: true,
	}
}

func (a *app) cancelActiveScenario() {
	a.mu.Lock()
	run := a.active
	if run == nil {
		run = a.starting
	}
	if run == nil {
		a.mu.Unlock()
		return
	}
	a.scenarioStatus[run.name] = statusStopping
	a.mu.Unlock()

	run.cancel()
}

func (a *app) resetCircuitBreakers(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://gateway:9090/admin/circuit-breakers/reset", nil)
	if err != nil {
		return fmt.Errorf("build circuit reset request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.adminToken)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call circuit reset endpoint: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("circuit reset endpoint returned %d", resp.StatusCode)
	}

	return nil
}

func (a *app) flushRedisRateLimitKeys(ctx context.Context) error {
	if a.redis == nil {
		return nil
	}

	var cursor uint64
	for {
		keys, nextCursor, err := a.redis.Scan(ctx, cursor, "rate_limit:*", 100)
		if err != nil {
			return fmt.Errorf("scan redis rate limit keys: %w", err)
		}
		if len(keys) > 0 {
			if err := a.redis.Del(ctx, keys...); err != nil {
				return fmt.Errorf("delete redis rate limit keys: %w", err)
			}
		}
		if nextCursor == 0 {
			return nil
		}
		cursor = nextCursor
	}
}

func (a *app) verifyServiceHealth(ctx context.Context) error {
	errCh := make(chan error, len(serviceEndpoints))
	for _, endpoint := range serviceEndpoints {
		go func(endpoint serviceEndpoint) {
			errCh <- a.waitForHealthy(ctx, endpoint)
		}(endpoint)
	}

	for range serviceEndpoints {
		if err := <-errCh; err != nil {
			return err
		}
	}

	return nil
}

func (a *app) waitForHealthy(ctx context.Context, endpoint serviceEndpoint) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.URL+"/health", nil)
		if err != nil {
			return fmt.Errorf("build health request for %s: %w", endpoint.Name, err)
		}

		resp, err := a.httpClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("%s health check failed: %w", endpoint.Name, err)
			}
			return fmt.Errorf("%s health check returned non-200", endpoint.Name)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func partialReset(step string, err error) resetResponse {
	return resetResponse{
		Status:     "partial",
		FailedStep: step,
		Details:    err.Error(),
	}
}
