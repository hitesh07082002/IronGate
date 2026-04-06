package telemetry

import (
	"context"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestInit_NoopWhenEnvUnset(t *testing.T) {
	t.Setenv(otlpEndpointEnvVar, "")

	tp, shutdown := Init(context.Background(), "irongate", "phase9")
	tracer := tp.Tracer("test")
	_, span := tracer.Start(context.Background(), "noop-span")
	span.End()

	if span.IsRecording() {
		t.Fatal("expected noop tracer provider when OTEL_EXPORTER_OTLP_ENDPOINT is unset")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown noop tracer provider: %v", err)
	}
}

func TestInit_UnreachableCollector(t *testing.T) {
	t.Setenv(otlpEndpointEnvVar, "localhost:19999")
	t.Setenv(otelSamplerEnvVar, alwaysOnSampler)

	start := time.Now()
	tp, shutdown := Init(context.Background(), "irongate", "phase9")
	if elapsed := time.Since(start); elapsed > 6*time.Second {
		t.Fatalf("expected Init to return within 6s, took %s", elapsed)
	}

	tracer := tp.Tracer("test")
	_, span := tracer.Start(context.Background(), "reachable-check")
	span.End()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = shutdown(shutdownCtx)
}

func TestTracerOrNoop_NilProvider(t *testing.T) {
	tracer := TracerOrNoop(nil, "test-tracer")
	_, span := tracer.Start(context.Background(), "nil-provider")
	span.End()
}

func TestTracerOrNoop_RealProvider(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	tracer := TracerOrNoop(tp, "test-tracer")
	_, span := tracer.Start(context.Background(), "real-provider")
	span.End()

	if got := len(recorder.Ended()); got != 1 {
		t.Fatalf("expected one ended span from real tracer provider, got %d", got)
	}
}

func TestOTLPInsecure_DefaultsFalse(t *testing.T) {
	t.Setenv(otlpInsecureEnvVar, "")
	if otlpInsecure() {
		t.Fatal("expected OTLP insecure mode disabled by default")
	}
}

func TestOTLPInsecure_True(t *testing.T) {
	t.Setenv(otlpInsecureEnvVar, "true")
	if !otlpInsecure() {
		t.Fatal("expected OTLP insecure mode when env var is true")
	}
}
