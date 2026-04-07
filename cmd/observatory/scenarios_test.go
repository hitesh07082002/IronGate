package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadScenariosSkipsInvalidFiles(t *testing.T) {
	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, "valid.yaml"), []byte(`
name: "happy-path"
display_name: "Happy Path"
category: baseline
duration_options: [30, 60]
intensity_options:
  mild: { rps: 10 }
k6_script: "scenarios/k6/happy-path.js"
`), 0o644); err != nil {
		t.Fatalf("write valid scenario: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "invalid.yaml"), []byte(`
display_name: "Missing Name"
duration_options: [30]
intensity_options:
  mild: { rps: 10 }
k6_script: "scenarios/k6/happy-path.js"
`), 0o644); err != nil {
		t.Fatalf("write invalid scenario: %v", err)
	}

	scenarios, err := loadScenarios(root)
	if err != nil {
		t.Fatalf("load scenarios: %v", err)
	}
	if len(scenarios) != 1 {
		t.Fatalf("expected 1 valid scenario, got %d", len(scenarios))
	}
	if _, ok := scenarios["happy-path"]; !ok {
		t.Fatalf("expected happy-path scenario to load")
	}
}

func TestScenarioResolveRunClampsDuration(t *testing.T) {
	scenario := &Scenario{
		Name:            "circuit-breaker-recovery",
		DurationOptions: []int{60, 120, 300},
		IntensityOptions: map[string]IntensityOption{
			"moderate": {RPS: 100},
		},
	}

	intensity, duration, err := scenario.ResolveRun(runParams{
		Intensity: "moderate",
		Duration:  999,
	})
	if err != nil {
		t.Fatalf("resolve run: %v", err)
	}
	if intensity.RPS != 100 {
		t.Fatalf("expected rps 100, got %d", intensity.RPS)
	}
	if duration != 300 {
		t.Fatalf("expected duration 300, got %d", duration)
	}
}
