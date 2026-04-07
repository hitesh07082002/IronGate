package testutil

import "testing"

func TestRedisAddrSkipsWhenUnset(t *testing.T) {
	skipped := false

	t.Run("unset redis addr", func(t *testing.T) {
		t.Setenv("IRONGATE_TEST_REDIS_ADDR", "")

		defer func() {
			skipped = t.Skipped()
		}()

		RedisAddr(t)
	})

	if !skipped {
		t.Fatal("expected RedisAddr to skip when IRONGATE_TEST_REDIS_ADDR is unset")
	}
}
