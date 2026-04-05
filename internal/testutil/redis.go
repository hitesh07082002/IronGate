package testutil

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func RedisAddr(t testing.TB) string {
	t.Helper()

	address := strings.TrimSpace(os.Getenv("IRONGATE_TEST_REDIS_ADDR"))
	if address == "" {
		t.Skip("IRONGATE_TEST_REDIS_ADDR is not set; skipping Redis integration test")
	}

	return address
}

func RedisClient(t testing.TB) *redis.Client {
	t.Helper()

	client := redis.NewClient(&redis.Options{Addr: RedisAddr(t)})
	t.Cleanup(func() {
		_ = client.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping test redis: %v", err)
	}

	return client
}
