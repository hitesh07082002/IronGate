package main

import (
	"encoding/json"
	"errors"
	"net/http"
)

type scenarioListItem struct {
	Name             string                     `json:"name"`
	DisplayName      string                     `json:"display_name"`
	Category         string                     `json:"category"`
	IntensityOptions map[string]IntensityOption `json:"intensity_options"`
	DurationOptions  []int                      `json:"duration_options"`
}

func (a *app) handleListScenarios(w http.ResponseWriter, _ *http.Request) {
	items := make([]scenarioListItem, 0, len(a.scenarios))
	for _, scenario := range sortedScenarios(a.scenarios) {
		items = append(items, scenarioListItem{
			Name:             scenario.Name,
			DisplayName:      scenario.DisplayName,
			Category:         scenario.Category,
			IntensityOptions: scenario.IntensityOptions,
			DurationOptions:  append([]int(nil), scenario.DurationOptions...),
		})
	}

	writeJSON(w, http.StatusOK, items)
}

func (a *app) handleGetScenario(w http.ResponseWriter, r *http.Request) {
	scenario, ok := a.scenarios[r.PathValue("name")]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "scenario not found")
		return
	}

	writeJSON(w, http.StatusOK, scenario)
}

func (a *app) handleScenarioStatuses(w http.ResponseWriter, _ *http.Request) {
	statuses := make(map[string]scenarioStatus, len(a.scenarios))
	for _, scenario := range sortedScenarios(a.scenarios) {
		statuses[scenario.Name] = a.scenarioStatusFor(scenario.Name)
	}

	writeJSON(w, http.StatusOK, statuses)
}

func (a *app) handleScenarioStatus(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := a.scenarios[name]; !ok {
		writeAPIError(w, http.StatusNotFound, "scenario not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]scenarioStatus{
		"status": a.scenarioStatusFor(name),
	})
}

func (a *app) handleRunScenario(w http.ResponseWriter, r *http.Request) {
	scenario, ok := a.scenarios[r.PathValue("name")]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "scenario not found")
		return
	}

	var params runParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := a.startScenario(scenario, params); err != nil {
		switch {
		case errors.Is(err, errScenarioAlreadyRunning):
			writeAPIError(w, http.StatusConflict, err.Error())
		case errors.Is(err, errInvalidRunParams):
			writeAPIError(w, http.StatusBadRequest, err.Error())
		default:
			writeAPIError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func (a *app) handleStopScenario(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := a.scenarios[name]; !ok {
		writeAPIError(w, http.StatusNotFound, "scenario not found")
		return
	}

	err := a.stopScenario(r.Context(), name)
	if errors.Is(err, errScenarioNotRunning) {
		writeJSON(w, http.StatusOK, map[string]scenarioStatus{"status": a.scenarioStatusFor(name)})
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]scenarioStatus{"status": statusStopping})
}
