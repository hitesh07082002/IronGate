package main

import (
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEventHubPublishSubscribeAndHandleEvents(t *testing.T) {
	hub := NewEventHub(newTestLogger())
	oldEvent := Event{TS: time.Now().UTC().Add(-10 * time.Minute), Type: "old", Message: "old"}
	newEvent := Event{TS: time.Now().UTC(), Type: "new", Message: "new"}

	hub.Publish(oldEvent)
	hub.Publish(newEvent)

	snapshot, eventsCh, cancel := hub.Subscribe()
	defer cancel()

	if len(snapshot) != 1 || snapshot[0].Type != "new" {
		t.Fatalf("expected pruned snapshot with latest event, got %#v", snapshot)
	}

	liveEvent := Event{TS: time.Now().UTC(), Type: "live", Message: "live"}
	hub.Publish(liveEvent)
	select {
	case got := <-eventsCh:
		if got.Type != "live" {
			t.Fatalf("published event type = %q, want live", got.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscribed event")
	}

	app := &app{eventHub: hub}
	reqCtx, cancelReq := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(reqCtx)
	recorder := httptest.NewRecorder()

	go func() {
		time.Sleep(20 * time.Millisecond)
		hub.Publish(Event{TS: time.Now().UTC(), Type: "streamed", Message: "streamed"})
		time.Sleep(20 * time.Millisecond)
		cancelReq()
	}()

	app.handleEvents(recorder, req)
	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("events content-type = %q, want text/event-stream", got)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"type":"new"`) || !strings.Contains(body, `"type":"streamed"`) {
		t.Fatalf("expected snapshot and live SSE events, body=%q", body)
	}
}

func TestParseEventLineAndSamplingHelpers(t *testing.T) {
	t.Setenv(demoTokenEnvVar, "demo-token")
	t.Setenv(adminTokenEnvVar, "admin-token")

	if _, ok, err := parseEventLine([]byte("not-json")); err != nil || ok {
		t.Fatalf("invalid JSON parse returned ok=%v err=%v", ok, err)
	}
	if _, ok, err := parseEventLine([]byte(`{"msg":"missing type"}`)); err != nil || ok {
		t.Fatalf("missing type parse returned ok=%v err=%v", ok, err)
	}

	event, ok, err := parseEventLine([]byte(`{
		"time":"2026-04-07T12:00:00Z",
		"level":"WARN",
		"msg":"retry happened",
		"type":"retry_attempt",
		"attrs":{
			"jwt":"eyJ.header.payload",
			"demo":"demo-token",
			"nested":{"admin":"admin-token"},
			"items":["plain","eyJ.another.token"]
		}
	}`))
	if err != nil || !ok {
		t.Fatalf("parseEventLine returned ok=%v err=%v", ok, err)
	}
	if event.Type != "retry_attempt" || event.Message != "retry happened" || event.Level != "WARN" {
		t.Fatalf("unexpected parsed event: %#v", event)
	}
	if event.Attrs["jwt"] != "[redacted-jwt]" || event.Attrs["demo"] != "[redacted-secret]" {
		t.Fatalf("expected top-level attrs to be sanitized: %#v", event.Attrs)
	}
	nested := event.Attrs["nested"].(map[string]any)
	if nested["admin"] != "[redacted-secret]" {
		t.Fatalf("expected nested secret to be sanitized: %#v", nested)
	}
	items := event.Attrs["items"].([]any)
	if items[1] != "[redacted-jwt]" {
		t.Fatalf("expected list jwt to be sanitized: %#v", items)
	}

	app := &app{eventHub: NewEventHub(newTestLogger())}
	app.eventHub.rng = rand.New(rand.NewSource(1))
	_ = app.shouldSampleEvent("request_success")
	_ = app.shouldSampleEvent("circuit_rejected")
	if !app.shouldSampleEvent("scenario_stopped") {
		t.Fatal("expected non-sampled event types to always pass")
	}
	if app.samplePercent(0) {
		t.Fatal("expected zero sample rate to fail")
	}
	if !app.samplePercent(1) {
		t.Fatal("expected full sample rate to pass")
	}

	if got := nextBackoff(time.Second); got != 2*time.Second {
		t.Fatalf("nextBackoff(1s) = %s, want 2s", got)
	}
	if got := nextBackoff(2 * time.Second); got != 5*time.Second {
		t.Fatalf("nextBackoff(2s) = %s, want 5s", got)
	}
	if got := nextBackoff(10 * time.Second); got != 30*time.Second {
		t.Fatalf("nextBackoff(other) = %s, want 30s", got)
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepContext(canceledCtx, time.Millisecond) {
		t.Fatal("expected canceled context sleep to fail")
	}
	if !sleepContext(context.Background(), time.Millisecond) {
		t.Fatal("expected uncanceled context sleep to succeed")
	}
}

func TestWriteSSEEventProducesJSONPayload(t *testing.T) {
	recorder := httptest.NewRecorder()
	err := writeSSEEvent(recorder, Event{
		TS:      time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC),
		Type:    "scenario_started",
		Message: "started",
		Attrs:   map[string]any{"scenario": "happy-path"},
	})
	if err != nil {
		t.Fatalf("writeSSEEvent: %v", err)
	}

	raw := strings.TrimPrefix(strings.TrimSpace(recorder.Body.String()), "data: ")
	var event Event
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatalf("decode SSE payload: %v", err)
	}
	if event.Type != "scenario_started" {
		t.Fatalf("SSE event type = %q, want scenario_started", event.Type)
	}
}
