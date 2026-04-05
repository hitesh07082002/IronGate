package common

import "testing"

func TestServicePortFromEnvUsesOverride(t *testing.T) {
	t.Setenv("PORT", "8093")

	if got := ServicePortFromEnv(8083); got != 8093 {
		t.Fatalf("expected env override port 8093, got %d", got)
	}
}

func TestServicePortFromEnvFallsBackOnInvalidValue(t *testing.T) {
	t.Setenv("PORT", "invalid")

	if got := ServicePortFromEnv(8083); got != 8083 {
		t.Fatalf("expected default port 8083 on invalid env, got %d", got)
	}
}

func TestServicePortFromEnvFallsBackOnOutOfRangeValue(t *testing.T) {
	t.Setenv("PORT", "70000")

	if got := ServicePortFromEnv(8083); got != 8083 {
		t.Fatalf("expected default port 8083 on out-of-range env, got %d", got)
	}
}
