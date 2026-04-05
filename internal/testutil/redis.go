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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping test redis: %v", err)
	}

	t.Cleanup(func() {
		_ = client.Close()
	})

	return client
}

func FlushRedis(t testing.TB, client *redis.Client) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("flush test redis: %v", err)
	}
}
