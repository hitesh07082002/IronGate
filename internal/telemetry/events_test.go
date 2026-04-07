package telemetry

import (
	"context"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

type captureHandler struct {
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (h *captureHandler) Handle(_ context.Context, record slog.Record) error {
	h.records = append(h.records, record.Clone())
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler {
	return h
}

func (h *captureHandler) WithGroup(string) slog.Handler {
	return h
}

func recordAttrs(record slog.Record) map[string]any {
	attrs := make(map[string]any)
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.Any()
		return true
	})
	return attrs
}

func TestLogGatewayEventSanitizesAttrs(t *testing.T) {
	t.Setenv("DEMO_TOKEN", "demo-secret")
	t.Setenv("ADMIN_TOKEN", "admin-secret")

	handler := &captureHandler{}
	logger := slog.New(handler)

	LogGatewayEvent(logger, slog.LevelWarn, "retry_attempt", "retry scheduled", map[string]any{
		"jwt":    "eyJ.header.payload",
		"demo":   "demo-secret",
		"nested": map[string]any{"admin": "admin-secret"},
		"items":  []string{"plain", "demo-secret"},
	})

	if len(handler.records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(handler.records))
	}
	attrs := recordAttrs(handler.records[0])
	if attrs["type"] != "retry_attempt" {
		t.Fatalf("record type = %#v, want retry_attempt", attrs["type"])
	}

	sanitized := attrs["attrs"].(map[string]any)
	if sanitized["jwt"] != "[redacted-jwt]" || sanitized["demo"] != "[redacted-secret]" {
		t.Fatalf("expected attrs to be sanitized: %#v", sanitized)
	}
	nested := sanitized["nested"].(map[string]any)
	if nested["admin"] != "[redacted-secret]" {
		t.Fatalf("expected nested secret redaction, got %#v", nested)
	}
	items := sanitized["items"].([]string)
	if items[1] != "[redacted-secret]" {
		t.Fatalf("expected list secret redaction, got %#v", items)
	}

	LogGatewayEvent(logger, slog.LevelInfo, "   ", "ignored", nil)
	if len(handler.records) != 1 {
		t.Fatalf("expected blank event type to be ignored, got %d records", len(handler.records))
	}

	previous := slog.Default()
	t.Cleanup(func() {
		slog.SetDefault(previous)
	})
	defaultHandler := &captureHandler{}
	slog.SetDefault(slog.New(defaultHandler))
	LogGatewayEvent(nil, slog.LevelInfo, "request_success", "done", map[string]any{"status": "ok"})
	if len(defaultHandler.records) != 1 {
		t.Fatal("expected nil logger to use slog default logger")
	}
}

func TestTraceIDAndSanitizers(t *testing.T) {
	t.Setenv("DEMO_TOKEN", "demo-secret")
	t.Setenv("ADMIN_TOKEN", "admin-secret")

	if got := TraceIDFromContext(nil); got != "" {
		t.Fatalf("TraceIDFromContext(nil) = %q, want empty", got)
	}
	if got := TraceIDFromContext(context.Background()); got != "" {
		t.Fatalf("TraceIDFromContext(background) = %q, want empty", got)
	}

	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanCtx)
	if got := TraceIDFromContext(ctx); got != "0102030405060708090a0b0c0d0e0f10" {
		t.Fatalf("TraceIDFromContext(valid) = %q", got)
	}

	if got := sanitizeEventString("eyJ.header.payload"); got != "[redacted-jwt]" {
		t.Fatalf("sanitizeEventString(jwt) = %q", got)
	}
	if got := sanitizeEventString("demo-secret"); got != "[redacted-secret]" {
		t.Fatalf("sanitizeEventString(secret) = %q", got)
	}
	if got := sanitizeEventString("plain-value"); got != "plain-value" {
		t.Fatalf("sanitizeEventString(plain) = %q", got)
	}

	sanitized := sanitizeEventValue(map[string]any{
		"jwt":   "eyJ.header.payload",
		"items": []string{"demo-secret", "plain"},
		"list":  []any{"admin-secret", map[string]any{"token": "demo-secret"}},
	}).(map[string]any)
	if sanitized["jwt"] != "[redacted-jwt]" {
		t.Fatalf("expected jwt redaction, got %#v", sanitized["jwt"])
	}
	items := sanitized["items"].([]string)
	if items[0] != "[redacted-secret]" {
		t.Fatalf("expected list redaction, got %#v", items)
	}
	list := sanitized["list"].([]any)
	if list[0] != "[redacted-secret]" {
		t.Fatalf("expected []any redaction, got %#v", list)
	}
	nested := list[1].(map[string]any)
	if nested["token"] != "[redacted-secret]" {
		t.Fatalf("expected nested redaction, got %#v", nested)
	}

	if !eventSecretMatch("admin-secret") {
		t.Fatal("expected admin secret match to be detected")
	}
	if eventSecretMatch("totally-safe") {
		t.Fatal("expected non-secret value not to match")
	}
	if !subtleStringMatch("abc", "abc") {
		t.Fatal("expected equal strings to match")
	}
	if subtleStringMatch("abc", "abcd") {
		t.Fatal("expected length mismatch to fail")
	}
}
