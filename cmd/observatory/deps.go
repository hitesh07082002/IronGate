package main

import (
	"context"
	"io"

	dockercontainer "github.com/docker/docker/api/types/container"
	dockerimage "github.com/docker/docker/api/types/image"
	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/redis/go-redis/v9"
)

type dockerClient interface {
	ContainerCreate(ctx context.Context, config *dockercontainer.Config, hostConfig *dockercontainer.HostConfig, networkingConfig *dockernetwork.NetworkingConfig, platform *ocispec.Platform, containerName string) (dockercontainer.CreateResponse, error)
	ContainerInspect(ctx context.Context, containerID string) (dockercontainer.InspectResponse, error)
	ContainerList(ctx context.Context, options dockercontainer.ListOptions) ([]dockercontainer.Summary, error)
	ContainerLogs(ctx context.Context, containerID string, options dockercontainer.LogsOptions) (io.ReadCloser, error)
	ContainerRemove(ctx context.Context, containerID string, options dockercontainer.RemoveOptions) error
	ContainerStart(ctx context.Context, containerID string, options dockercontainer.StartOptions) error
	ContainerStop(ctx context.Context, containerID string, options dockercontainer.StopOptions) error
	ContainerWait(ctx context.Context, containerID string, condition dockercontainer.WaitCondition) (<-chan dockercontainer.WaitResponse, <-chan error)
	ImageInspectWithRaw(ctx context.Context, imageID string) (dockerimage.InspectResponse, []byte, error)
	ImagePull(ctx context.Context, ref string, options dockerimage.PullOptions) (io.ReadCloser, error)
	Close() error
}

var _ dockerClient = (*client.Client)(nil)

type rateLimitStore interface {
	Close() error
	Del(ctx context.Context, keys ...string) error
	Scan(ctx context.Context, cursor uint64, match string, count int64) ([]string, uint64, error)
}

type redisRateLimitStore struct {
	client *redis.Client
}

func newRedisRateLimitStore(client *redis.Client) *redisRateLimitStore {
	return &redisRateLimitStore{client: client}
}

func (s *redisRateLimitStore) Close() error {
	if s == nil || s.client == nil {
		return nil
	}

	return s.client.Close()
}

func (s *redisRateLimitStore) Del(ctx context.Context, keys ...string) error {
	if s == nil || s.client == nil || len(keys) == 0 {
		return nil
	}

	return s.client.Del(ctx, keys...).Err()
}

func (s *redisRateLimitStore) Scan(ctx context.Context, cursor uint64, match string, count int64) ([]string, uint64, error) {
	if s == nil || s.client == nil {
		return nil, 0, nil
	}

	return s.client.Scan(ctx, cursor, match, count).Result()
}
