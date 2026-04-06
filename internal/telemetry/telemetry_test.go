package telemetry

import (
	"context"
	"net/http"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
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

func TestInit_NoopWhenEnvUnsetStillConfiguresTraceContextPropagation(t *testing.T) {
	t.Setenv(otlpEndpointEnvVar, "")

	previous := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTextMapPropagator(previous)
	})
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())

	_, shutdown := Init(context.Background(), "irongate", "phase9")
	defer func() {
		_ = shutdown(context.Background())
	}()

	carrier := propagation.HeaderCarrier(http.Header{})
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     [8]byte{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	}))
	otel.GetTextMapPropagator().Inject(ctx, carrier)

	if got := carrier.Get("Traceparent"); got == "" {
		t.Fatal("expected Init to leave W3C trace propagation enabled in noop mode")
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

func TestOTLPEndpointUsesURL(t *testing.T) {
	if otlpEndpointUsesURL("otel-collector:4317") {
		t.Fatal("expected host:port endpoint to avoid URL mode")
	}
	if !otlpEndpointUsesURL("https://otel-collector:4317/v1/traces") {
		t.Fatal("expected URL-shaped endpoint to use URL mode")
	}
}

func TestTraceSampler_AlwaysOff(t *testing.T) {
	t.Setenv(otelSamplerEnvVar, alwaysOffSampler)

	decision := traceSampler().ShouldSample(sdktrace.SamplingParameters{
		ParentContext: context.Background(),
		Name:          "always-off",
	})
	if decision.Decision != sdktrace.Drop {
		t.Fatalf("expected always_off sampler to drop spans, got %v", decision.Decision)
	}
}

func TestTraceSampler_DefaultMatchesDefaultSampler(t *testing.T) {
	t.Setenv(otelSamplerEnvVar, "")

	params := sdktrace.SamplingParameters{
		ParentContext: context.Background(),
		TraceID:       trace.TraceID{1, 2, 3, 4},
		Name:          "default",
	}
	if got, want := traceSampler().ShouldSample(params).Decision, defaultTraceSampler().ShouldSample(params).Decision; got != want {
		t.Fatalf("expected default sampler decision %v, got %v", want, got)
	}
}

func TestTraceSampler_AlwaysOn(t *testing.T) {
	t.Setenv(otelSamplerEnvVar, alwaysOnSampler)

	decision := traceSampler().ShouldSample(sdktrace.SamplingParameters{
		ParentContext: context.Background(),
		Name:          "always-on",
	})
	if decision.Decision != sdktrace.RecordAndSample {
		t.Fatalf("expected always_on sampler to record and sample spans, got %v", decision.Decision)
	}
}

func TestTraceSampler_TraceIDRatioRespectsSamplerArg(t *testing.T) {
	t.Setenv(otelSamplerEnvVar, traceIDRatioSampler)
	t.Setenv(otelSamplerArgEnvVar, "0")

	decision := traceSampler().ShouldSample(sdktrace.SamplingParameters{
		ParentContext: context.Background(),
		TraceID:       trace.TraceID{1},
		Name:          "ratio-zero",
	})
	if decision.Decision != sdktrace.Drop {
		t.Fatalf("expected traceidratio=0 to drop spans, got %v", decision.Decision)
	}
}

func TestTraceSampler_ParentBasedTraceIDRatioDefaultsToOneWhenArgMissing(t *testing.T) {
	t.Setenv(otelSamplerEnvVar, parentBasedTraceIDRatioSampler)
	t.Setenv(otelSamplerArgEnvVar, "")

	decision := traceSampler().ShouldSample(sdktrace.SamplingParameters{
		ParentContext: context.Background(),
		TraceID:       trace.TraceID{2},
		Name:          "ratio-default",
	})
	if decision.Decision != sdktrace.RecordAndSample {
		t.Fatalf("expected parentbased_traceidratio with default arg to sample root spans, got %v", decision.Decision)
	}
}

func TestTraceSampler_ParentBasedAlwaysOffDropsRootSpans(t *testing.T) {
	t.Setenv(otelSamplerEnvVar, parentBasedAlwaysOffSampler)

	decision := traceSampler().ShouldSample(sdktrace.SamplingParameters{
		ParentContext: context.Background(),
		Name:          "parentbased-off",
	})
	if decision.Decision != sdktrace.Drop {
		t.Fatalf("expected parentbased_always_off to drop root spans, got %v", decision.Decision)
	}
}

func TestTraceSampler_InvalidSamplerFallsBackToDefault(t *testing.T) {
	t.Setenv(otelSamplerEnvVar, "definitely-not-valid")

	params := sdktrace.SamplingParameters{
		ParentContext: context.Background(),
		TraceID:       trace.TraceID{9, 9, 9, 9},
		Name:          "fallback",
	}
	if got, want := traceSampler().ShouldSample(params).Decision, defaultTraceSampler().ShouldSample(params).Decision; got != want {
		t.Fatalf("expected invalid sampler to fall back to default decision %v, got %v", want, got)
	}
}

func TestTraceSamplerRatio_InvalidFallsBack(t *testing.T) {
	t.Setenv(otelSamplerArgEnvVar, "nope")
	if got := traceSamplerRatio(0.25); got != 0.25 {
		t.Fatalf("expected invalid sampler ratio to fall back to 0.25, got %v", got)
	}
}

func TestHashAttr_IsDeterministic(t *testing.T) {
	if got := HashAttr("user-42"); got != hashAttr("user-42") {
		t.Fatalf("expected HashAttr to delegate to hashAttr, got %q", got)
	}
	if got := HashAttr("user-42"); len(got) != 8 {
		t.Fatalf("expected 8-char hashed attribute, got %q", got)
	}
	if first, second := HashAttr("user-42"), HashAttr("user-42"); first != second {
		t.Fatalf("expected deterministic hashed attribute, got %q and %q", first, second)
	}
}

func TestWarnOnceExporter_ReturnsUnderlyingError(t *testing.T) {
	exporter := &warnOnceExporter{
		SpanExporter: stubExporter{err: context.DeadlineExceeded},
	}

	if err := exporter.ExportSpans(context.Background(), nil); err == nil {
		t.Fatal("expected wrapped exporter to return the underlying error")
	}
	if err := exporter.ExportSpans(context.Background(), nil); err == nil {
		t.Fatal("expected wrapped exporter to keep returning the underlying error")
	}
}

type stubExporter struct {
	err error
}

func (e stubExporter) ExportSpans(_ context.Context, _ []sdktrace.ReadOnlySpan) error {
	return e.err
}

func (e stubExporter) Shutdown(context.Context) error {
	return nil
}
