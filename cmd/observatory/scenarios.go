package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	maxScenarioRPS      = 500
	maxScenarioDuration = 300
)

var errInvalidRunParams = errors.New("invalid run params")

type Scenario struct {
	Name             string                     `json:"name" yaml:"name"`
	DisplayName      string                     `json:"display_name" yaml:"display_name"`
	Description      string                     `json:"description" yaml:"description"`
	WhatYouLearn     string                     `json:"what_you_learn" yaml:"what_you_learn"`
	WhatToWatch      string                     `json:"what_to_watch" yaml:"what_to_watch"`
	Category         string                     `json:"category" yaml:"category"`
	DurationOptions  []int                      `json:"duration_options" yaml:"duration_options"`
	IntensityOptions map[string]IntensityOption `json:"intensity_options" yaml:"intensity_options"`
	ChaosSequence    []ChaosStep                `json:"chaos_sequence,omitempty" yaml:"chaos_sequence"`
	ExpectedSignals  []map[string]any           `json:"expected_signals,omitempty" yaml:"expected_signals"`
	K6Script         string                     `json:"k6_script" yaml:"k6_script"`
	ResetActions     []map[string]any           `json:"reset_actions,omitempty" yaml:"reset_actions"`
}

type IntensityOption struct {
	RPS int `json:"rps" yaml:"rps"`
}

type ChaosStep struct {
	AtSeconds int    `json:"at_seconds" yaml:"at_seconds"`
	Action    string `json:"action" yaml:"action"`
	Target    string `json:"target,omitempty" yaml:"target"`
}

func loadScenarios(root string) (map[string]*Scenario, error) {
	pattern := filepath.Join(root, "*.yaml")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob scenarios: %w", err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no scenario yaml files found at %s", root)
	}

	scenarios := make(map[string]*Scenario, len(matches))
	var invalid []error
	for _, path := range matches {
		scenario, err := loadScenarioFile(path)
		if err != nil {
			invalid = append(invalid, err)
			continue
		}
		scenarios[scenario.Name] = scenario
	}
	if len(scenarios) == 0 {
		return nil, fmt.Errorf("all scenarios invalid: %v", invalid)
	}

	return scenarios, nil
}

func loadScenarioFile(path string) (*Scenario, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read scenario %s: %w", path, err)
	}

	var scenario Scenario
	if err := yaml.Unmarshal(raw, &scenario); err != nil {
		return nil, fmt.Errorf("decode scenario %s: %w", path, err)
	}
	if err := scenario.Validate(); err != nil {
		return nil, fmt.Errorf("validate scenario %s: %w", path, err)
	}

	return &scenario, nil
}

func (s *Scenario) Validate() error {
	if s == nil {
		return errors.New("scenario is nil")
	}
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("scenario name is required")
	}
	if strings.TrimSpace(s.DisplayName) == "" {
		return errors.New("display_name is required")
	}
	if strings.TrimSpace(s.Category) == "" {
		return errors.New("category is required")
	}
	if len(s.DurationOptions) == 0 {
		return errors.New("duration_options is required")
	}
	if len(s.IntensityOptions) == 0 {
		return errors.New("intensity_options is required")
	}
	if strings.TrimSpace(s.K6Script) == "" {
		return errors.New("k6_script is required")
	}
	if strings.Contains(s.K6Script, "..") {
		return errors.New("k6_script must not escape scenarios/k6")
	}

	for index, duration := range s.DurationOptions {
		if duration <= 0 {
			return fmt.Errorf("duration_options[%d] must be positive", index)
		}
		if duration > maxScenarioDuration {
			s.DurationOptions[index] = maxScenarioDuration
		}
	}

	for name, option := range s.IntensityOptions {
		if strings.TrimSpace(name) == "" {
			return errors.New("intensity option name is required")
		}
		if option.RPS <= 0 {
			return fmt.Errorf("intensity option %s must have positive rps", name)
		}
		if option.RPS > maxScenarioRPS {
			option.RPS = maxScenarioRPS
			s.IntensityOptions[name] = option
		}
	}

	for index, step := range s.ChaosSequence {
		if step.AtSeconds < 0 {
			return fmt.Errorf("chaos_sequence[%d] at_seconds must be non-negative", index)
		}
		if strings.TrimSpace(step.Action) == "" {
			return fmt.Errorf("chaos_sequence[%d] action is required", index)
		}
	}

	sort.Ints(s.DurationOptions)
	return nil
}

func (s *Scenario) ResolveRun(params runParams) (IntensityOption, int, error) {
	if s == nil {
		return IntensityOption{}, 0, errInvalidRunParams
	}

	intensity, ok := s.IntensityOptions[strings.TrimSpace(params.Intensity)]
	if !ok {
		return IntensityOption{}, 0, fmt.Errorf("%w: unknown intensity", errInvalidRunParams)
	}

	duration := params.Duration
	if duration > maxScenarioDuration {
		duration = maxScenarioDuration
	}
	if duration <= 0 || !containsDuration(s.DurationOptions, duration) {
		return IntensityOption{}, 0, fmt.Errorf("%w: unsupported duration", errInvalidRunParams)
	}

	return intensity, duration, nil
}

func containsDuration(options []int, value int) bool {
	for _, option := range options {
		if option == value {
			return true
		}
	}

	return false
}

func sortedScenarios(scenarios map[string]*Scenario) []*Scenario {
	names := make([]string, 0, len(scenarios))
	for name := range scenarios {
		names = append(names, name)
	}
	sort.Strings(names)

	ordered := make([]*Scenario, 0, len(names))
	for _, name := range names {
		ordered = append(ordered, scenarios[name])
	}

	return ordered
}
