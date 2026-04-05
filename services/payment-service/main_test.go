package main

import "testing"

func TestServicePortUsesEnvironmentOverride(t *testing.T) {
	t.Setenv("PORT", "8093")

	if got := servicePort(8083); got != 8093 {
		t.Fatalf("expected env override port 8093, got %d", got)
	}
}

func TestServicePortFallsBackToDefaultOnInvalidValue(t *testing.T) {
	t.Setenv("PORT", "invalid")

	if got := servicePort(8083); got != 8083 {
		t.Fatalf("expected default port 8083 on invalid env, got %d", got)
	}
}
