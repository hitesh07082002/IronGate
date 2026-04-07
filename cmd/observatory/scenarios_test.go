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

func TestScenarioValidateChaosParams(t *testing.T) {
	baseScenario := func(step ChaosStep) *Scenario {
		return &Scenario{
			Name:            "chaos-check",
			DisplayName:     "Chaos Check",
			Category:        "resilience",
			DurationOptions: []int{30},
			IntensityOptions: map[string]IntensityOption{
				"mild": {RPS: 10},
			},
			K6Script:      "scenarios/k6/happy-path.js",
			ChaosSequence: []ChaosStep{step},
		}
	}

	tests := []struct {
		name    string
		step    ChaosStep
		wantErr bool
	}{
		{
			name: "valid error inject",
			step: ChaosStep{AtSeconds: 5, Action: "error_inject", Target: "user-service-1", Params: map[string]any{"rate": 0.3}},
		},
		{
			name: "valid latency inject",
			step: ChaosStep{AtSeconds: 5, Action: "latency_inject", Target: "order-service-2", Params: map[string]any{"delay_ms": 500}},
		},
		{
			name: "valid add toxic",
			step: ChaosStep{AtSeconds: 5, Action: "add_toxic", Params: map[string]any{
				"type": "latency",
				"attributes": map[string]any{
					"latency": 500,
				},
			}},
		},
		{
			name: "valid remove toxic",
			step: ChaosStep{AtSeconds: 5, Action: "remove_toxic", Params: map[string]any{"type": "latency"}},
		},
		{
			name:    "missing rate rejected",
			step:    ChaosStep{AtSeconds: 5, Action: "error_inject", Target: "user-service-1"},
			wantErr: true,
		},
		{
			name:    "missing delay rejected",
			step:    ChaosStep{AtSeconds: 5, Action: "latency_inject", Target: "order-service-2"},
			wantErr: true,
		},
		{
			name:    "missing toxic attributes rejected",
			step:    ChaosStep{AtSeconds: 5, Action: "add_toxic", Params: map[string]any{"type": "latency"}},
			wantErr: true,
		},
		{
			name:    "missing toxic type rejected",
			step:    ChaosStep{AtSeconds: 5, Action: "remove_toxic", Params: map[string]any{}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := baseScenario(tt.step).Validate()
			if tt.wantErr && err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected scenario to validate, got %v", err)
			}
		})
	}
}

func TestLoadBuiltInScenariosIncludesFullCatalog(t *testing.T) {
	scenarios, err := loadScenarios(filepath.Join("..", "..", "scenarios"))
	if err != nil {
		t.Fatalf("load scenarios: %v", err)
	}

	want := []string{
		"auth-wall",
		"cascading-failure",
		"circuit-breaker-recovery",
		"happy-path",
		"latency-injection",
		"rate-limit-storm",
		"redis-impaired",
		"single-replica-death",
		"upstream-5xx-retry",
	}
	if len(scenarios) != len(want) {
		t.Fatalf("expected %d scenarios, got %d", len(want), len(scenarios))
	}
	for _, name := range want {
		if _, ok := scenarios[name]; !ok {
			t.Fatalf("expected scenario %q to be present", name)
		}
	}
}
