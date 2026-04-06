package testutil

import "testing"

func TestRedisAddrSkipsWhenUnset(t *testing.T) {
	t.Setenv("IRONGATE_TEST_REDIS_ADDR", "")

	defer func() {
		if recover() != nil {
			t.Fatal("expected RedisAddr skip, not panic")
		}
	}()

	RedisAddr(t)
}
