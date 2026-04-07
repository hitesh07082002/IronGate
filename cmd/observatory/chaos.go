package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	dockerfilters "github.com/docker/docker/api/types/filters"
)

func (a *app) executeChaos(ctx context.Context, scenario *Scenario) error {
	if scenario == nil || len(scenario.ChaosSequence) == 0 {
		return nil
	}

	startedAt := time.Now()
	for _, step := range scenario.ChaosSequence {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Until(startedAt.Add(time.Duration(step.AtSeconds) * time.Second))):
		}

		if err := a.executeChaosStep(ctx, step); err != nil {
			return err
		}
	}

	return nil
}

func (a *app) executeChaosStep(ctx context.Context, step ChaosStep) error {
	switch strings.TrimSpace(step.Action) {
	case "service_down":
		return a.stopServiceContainer(ctx, step.Target)
	case "service_up":
		targetURL, err := serviceURL(step.Target)
		if err != nil {
			return err
		}
		if err := a.startServiceContainer(ctx, step.Target); err != nil {
			return err
		}
		return a.waitForHealthy(ctx, serviceEndpoint{Name: step.Target, URL: targetURL})
	case "error_inject":
		targetURL, err := serviceURL(step.Target)
		if err != nil {
			return err
		}
		rate, err := chaosFloatParam(step.Params, "rate")
		if err != nil {
			return err
		}
		return a.postJSON(ctx, targetURL+"/chaos/errors", map[string]any{"rate": rate})
	case "latency_inject":
		targetURL, err := serviceURL(step.Target)
		if err != nil {
			return err
		}
		delayMS, err := chaosIntParam(step.Params, "delay_ms")
		if err != nil {
			return err
		}
		return a.postJSON(ctx, targetURL+"/chaos/latency", map[string]any{"delay_ms": delayMS})
	case "add_toxic":
		if a == nil || a.toxiproxy == nil {
			return fmt.Errorf("toxiproxy client is not configured")
		}
		toxicType, err := chaosStringParam(step.Params, "type")
		if err != nil {
			return err
		}
		attrs, err := chaosAttributesParam(step.Params, "attributes")
		if err != nil {
			return err
		}
		return a.toxiproxy.AddToxic(ctx, toxicType, attrs)
	case "remove_toxic":
		if a == nil || a.toxiproxy == nil {
			return fmt.Errorf("toxiproxy client is not configured")
		}
		toxicType, err := chaosStringParam(step.Params, "type")
		if err != nil {
			return err
		}
		return a.toxiproxy.RemoveToxic(ctx, toxicType)
	default:
		return fmt.Errorf("unsupported chaos action %q", step.Action)
	}
}

func (a *app) resetChaos(ctx context.Context) error {
	for _, endpoint := range serviceEndpoints {
		if err := a.startServiceContainer(ctx, endpoint.Name); err != nil {
			return err
		}
	}

	for _, endpoint := range serviceEndpoints {
		if err := a.waitForHealthy(ctx, endpoint); err != nil {
			return err
		}
	}

	errCh := make(chan error, len(serviceEndpoints))
	for _, endpoint := range serviceEndpoints {
		go func(endpoint serviceEndpoint) {
			errCh <- a.postJSON(ctx, endpoint.URL+"/chaos/reset", nil)
		}(endpoint)
	}

	for range serviceEndpoints {
		if err := <-errCh; err != nil {
			return err
		}
	}

	return nil
}

func (a *app) serviceContainerID(ctx context.Context, service string) (string, bool, error) {
	filterArgs := dockerfilters.NewArgs(
		dockerfilters.Arg("label", "com.docker.compose.project="+a.composeProject),
		dockerfilters.Arg("label", "com.docker.compose.service="+service),
	)

	containers, err := a.docker.ContainerList(ctx, dockercontainer.ListOptions{
		All:     true,
		Filters: filterArgs,
	})
	if err != nil {
		return "", false, fmt.Errorf("list container for %s: %w", service, err)
	}
	if len(containers) == 0 {
		return "", false, fmt.Errorf("container for %s not found", service)
	}

	return containers[0].ID, containers[0].State == "running", nil
}

func (a *app) stopServiceContainer(ctx context.Context, service string) error {
	containerID, running, err := a.serviceContainerID(ctx, service)
	if err != nil {
		return err
	}
	if !running {
		return nil
	}

	timeout := 5
	if err := a.docker.ContainerStop(ctx, containerID, dockercontainer.StopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("stop %s container: %w", service, err)
	}

	return nil
}

func (a *app) startServiceContainer(ctx context.Context, service string) error {
	containerID, running, err := a.serviceContainerID(ctx, service)
	if err != nil {
		return err
	}
	if running {
		return nil
	}

	if err := a.docker.ContainerStart(ctx, containerID, dockercontainer.StartOptions{}); err != nil {
		return fmt.Errorf("start %s container: %w", service, err)
	}

	return nil
}

func (a *app) postJSON(ctx context.Context, url string, payload any) error {
	var body []byte
	if payload != nil {
		var err error
		body, err = jsonMarshal(payload)
		if err != nil {
			return err
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request for %s: %w", url, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("post %s returned %d", url, resp.StatusCode)
	}

	return nil
}

func serviceURL(name string) (string, error) {
	for _, endpoint := range serviceEndpoints {
		if endpoint.Name == name {
			return endpoint.URL, nil
		}
	}

	return "", fmt.Errorf("unknown service target %q", name)
}

func jsonMarshal(payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal json payload: %w", err)
	}

	return raw, nil
}
