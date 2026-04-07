package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	dockercontainer "github.com/docker/docker/api/types/container"
	dockerfilters "github.com/docker/docker/api/types/filters"
	dockerimage "github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
)

const k6ImageRef = "grafana/k6:0.51.0"

type Runner struct {
	logger         *slog.Logger
	docker         dockerClient
	projectRoot    string
	composeProject string
}

func NewRunner(logger *slog.Logger, docker dockerClient, projectRoot, composeProject string) *Runner {
	if logger == nil {
		logger = slog.Default()
	}

	return &Runner{
		logger:         logger,
		docker:         docker,
		projectRoot:    projectRoot,
		composeProject: composeProject,
	}
}

func (r *Runner) Start(ctx context.Context, scenario *Scenario, intensityName string, rps, durationSeconds int, jwt string) (string, error) {
	if r == nil || r.docker == nil {
		return "", fmt.Errorf("docker client is not configured")
	}
	if scenario == nil {
		return "", fmt.Errorf("scenario is nil")
	}

	if err := r.ensureImage(ctx); err != nil {
		return "", err
	}

	scriptsDir := filepath.Join(r.projectRoot, "scenarios", "k6")
	relativeScriptPath := filepath.ToSlash(filepath.Clean(strings.TrimSpace(scenario.K6Script)))
	relativeScriptPath = strings.TrimPrefix(relativeScriptPath, "./")
	relativeScriptPath = strings.TrimPrefix(relativeScriptPath, "scenarios/k6/")
	relativeScriptPath = strings.TrimPrefix(relativeScriptPath, "/")
	scriptPath := "/scripts/" + relativeScriptPath
	containerName := fmt.Sprintf("irongate-k6-%s-%d", scenario.Name, time.Now().UnixNano())
	env := []string{
		"INTENSITY=" + strings.TrimSpace(intensityName),
		"RPS=" + fmt.Sprint(rps),
		"DURATION=" + fmt.Sprint(durationSeconds),
		"TARGET_URL=http://gateway:8080",
		"JWT=" + strings.TrimSpace(jwt),
		"TOKEN_POOL_SIZE=" + fmt.Sprint(k6TokenPoolSize(rps)),
		"LOGIN_SUBJECT_PREFIX=observatory-user",
		"LOGIN_ROLE=admin",
	}

	resp, err := r.docker.ContainerCreate(
		ctx,
		&dockercontainer.Config{
			Image: k6ImageRef,
			Cmd:   []string{"run", scriptPath},
			Env:   env,
			Labels: map[string]string{
				k6ManagedContainerLabel: k6ManagedContainerLabelTrue,
				k6ScenarioLabel:         scenario.Name,
			},
		},
		&dockercontainer.HostConfig{
			NetworkMode: dockercontainer.NetworkMode(r.composeProject + "_default"),
			Binds: []string{
				scriptsDir + ":/scripts:ro",
			},
		},
		nil,
		nil,
		containerName,
	)
	if err != nil {
		return "", fmt.Errorf("create k6 container: %w", err)
	}

	started := false
	defer func() {
		if started {
			return
		}
		_ = r.docker.ContainerRemove(context.Background(), resp.ID, dockercontainer.RemoveOptions{Force: true})
	}()

	if err := r.docker.ContainerStart(ctx, resp.ID, dockercontainer.StartOptions{}); err != nil {
		return "", fmt.Errorf("start k6 container: %w", err)
	}
	started = true

	go r.logContainerOutput(context.Background(), resp.ID)

	r.logger.Info("started k6 scenario", "scenario", scenario.Name, "container_id", resp.ID)
	return resp.ID, nil
}

func k6TokenPoolSize(rps int) int {
	if rps < 10 {
		return 10
	}
	if rps > 100 {
		return 100
	}
	return rps
}

func (r *Runner) Wait(ctx context.Context, containerID string) error {
	if strings.TrimSpace(containerID) == "" {
		return nil
	}
	if r == nil || r.docker == nil {
		return fmt.Errorf("docker client is not configured")
	}

	statusCh, errCh := r.docker.ContainerWait(ctx, containerID, dockercontainer.WaitConditionNotRunning)
	defer func() {
		_ = r.docker.ContainerRemove(context.Background(), containerID, dockercontainer.RemoveOptions{Force: true})
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		if err == nil {
			return nil
		}
		return fmt.Errorf("wait for k6 container: %w", err)
	case status := <-statusCh:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if status.StatusCode != 0 {
			return fmt.Errorf("k6 container exited with code %d", status.StatusCode)
		}
		return nil
	}
}

func (r *Runner) Stop(ctx context.Context, containerID string) error {
	if strings.TrimSpace(containerID) == "" {
		return nil
	}
	if r == nil || r.docker == nil {
		return fmt.Errorf("docker client is not configured")
	}

	timeout := 5
	if err := r.docker.ContainerStop(ctx, containerID, dockercontainer.StopOptions{Timeout: &timeout}); err != nil {
		if client.IsErrNotFound(err) {
			return nil
		}
		if !cerrdefs.IsConflict(err) || !strings.Contains(err.Error(), "is not running") {
			return fmt.Errorf("stop k6 container: %w", err)
		}
	}
	if err := r.docker.ContainerRemove(ctx, containerID, dockercontainer.RemoveOptions{Force: true, RemoveVolumes: true}); err != nil && !client.IsErrNotFound(err) {
		if cerrdefs.IsConflict(err) && strings.Contains(err.Error(), "already in progress") {
			return nil
		}
		return fmt.Errorf("remove k6 container: %w", err)
	}

	return nil
}

func (r *Runner) StopManagedContainers(ctx context.Context) error {
	if r == nil || r.docker == nil {
		return nil
	}

	filterArgs := dockerfilters.NewArgs(
		dockerfilters.Arg("label", k6ManagedContainerLabel+"="+k6ManagedContainerLabelTrue),
	)

	containers, err := r.docker.ContainerList(ctx, dockercontainer.ListOptions{
		All:     true,
		Filters: filterArgs,
	})
	if err != nil {
		return fmt.Errorf("list managed k6 containers: %w", err)
	}

	for _, listed := range containers {
		if err := r.Stop(ctx, listed.ID); err != nil && !client.IsErrNotFound(err) {
			return fmt.Errorf("stop managed k6 container %s: %w", listed.ID, err)
		}
	}

	return nil
}

func (r *Runner) ensureImage(ctx context.Context) error {
	if _, _, err := r.docker.ImageInspectWithRaw(ctx, k6ImageRef); err == nil {
		return nil
	}

	pullReader, err := r.docker.ImagePull(ctx, k6ImageRef, dockerimage.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull k6 image: %w", err)
	}
	defer pullReader.Close()

	if _, err := io.Copy(io.Discard, pullReader); err != nil {
		return fmt.Errorf("consume k6 image pull stream: %w", err)
	}

	return nil
}

func (r *Runner) logContainerOutput(ctx context.Context, containerID string) {
	if strings.TrimSpace(containerID) == "" {
		return
	}
	if r == nil || r.docker == nil {
		return
	}

	reader, err := r.docker.ContainerLogs(ctx, containerID, dockercontainer.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	})
	if err != nil {
		r.logger.Warn("failed to stream k6 container logs", "container_id", containerID, "error", err)
		return
	}
	defer reader.Close()

	_, _ = io.Copy(io.Discard, reader)
}
