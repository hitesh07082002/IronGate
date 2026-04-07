package main

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func TestDockerStreamParse(t *testing.T) {
	t.Setenv(demoTokenEnvVar, "demo-secret")
	t.Setenv(adminTokenEnvVar, "admin-secret")

	var stream bytes.Buffer
	writeDockerStdoutChunk(t, &stream, []byte(`{"time":"2026-04-07T12:00:00Z","level":"INFO","msg":"request completed successfully","type":"request_success","attrs":{"trace_id":"1234567890abcdef1234567890abcdef","jwt":"eyJ.header.payload","demo":"demo-secret"}}`+"\n"))
	writeDockerStdoutChunk(t, &stream, []byte(`{"time":"2026-04-07T12:00:01Z","level":"WARN","msg":"circuit OPEN on user-service-2:8091","type":"circuit_open","attrs":{"trace_id":"abcdef1234567890abcdef1234567890","admin":"admin-secret"}}`+"\n"))

	var events []Event
	if err := parseEvents(&stream, func(event Event) {
		events = append(events, event)
	}); err != nil {
		t.Fatalf("parse events: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != "request_success" {
		t.Fatalf("expected first event request_success, got %q", events[0].Type)
	}
	if got := events[0].Attrs["jwt"]; got != "[redacted-jwt]" {
		t.Fatalf("expected JWT attr sanitized, got %#v", got)
	}
	if got := events[0].Attrs["demo"]; got != "[redacted-secret]" {
		t.Fatalf("expected demo token sanitized, got %#v", got)
	}
	if got := events[1].Attrs["admin"]; got != "[redacted-secret]" {
		t.Fatalf("expected admin token sanitized, got %#v", got)
	}
	if got := events[1].TS; !got.Equal(time.Date(2026, 4, 7, 12, 0, 1, 0, time.UTC)) {
		t.Fatalf("unexpected parsed timestamp: %s", got)
	}
}

func writeDockerStdoutChunk(t *testing.T, buffer *bytes.Buffer, payload []byte) {
	t.Helper()

	header := make([]byte, 8)
	header[0] = 1
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	if _, err := buffer.Write(header); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := buffer.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
}
