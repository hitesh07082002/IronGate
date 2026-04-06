package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
)

const (
	otlpEndpointEnvVar = "OTEL_EXPORTER_OTLP_ENDPOINT"
	otelSamplerEnvVar  = "OTEL_TRACES_SAMPLER"

	alwaysOnSampler = "always_on"

	otlpDialTimeout   = 5 * time.Second
	otlpExportTimeout = 10 * time.Second
)

func Init(ctx context.Context, serviceName, version string) (trace.TracerProvider, func(context.Context) error) {
	endpoint := strings.TrimSpace(os.Getenv(otlpEndpointEnvVar))
	if endpoint == "" {
		return noop.NewTracerProvider(), func(context.Context) error { return nil }
	}
	if ctx == nil {
		ctx = context.Background()
	}

	exporter, err := otlptracegrpc.New(
		ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithDialOption(grpc.WithConnectParams(grpc.ConnectParams{
			MinConnectTimeout: otlpDialTimeout,
		})),
	)
	if err != nil {
		slog.Warn("failed to initialize OpenTelemetry exporter", "error", err)
		return noop.NewTracerProvider(), func(context.Context) error { return nil }
	}

	resource := sdkresource.NewSchemaless(
		attribute.String("service.name", serviceName),
		attribute.String("service.version", version),
	)
	processor := sdktrace.NewBatchSpanProcessor(
		&warnOnceExporter{SpanExporter: exporter},
		sdktrace.WithExportTimeout(otlpExportTimeout),
	)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resource),
		sdktrace.WithSampler(traceSampler()),
		sdktrace.WithSpanProcessor(processor),
	)

	otel.SetTextMapPropagator(propagation.TraceContext{})

	return tp, tp.Shutdown
}

func TracerOrNoop(tp trace.TracerProvider, name string) trace.Tracer {
	if tp == nil {
		return noop.NewTracerProvider().Tracer(name)
	}

	return tp.Tracer(name)
}

func hashAttr(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:8]
}

func HashAttr(value string) string {
	return hashAttr(value)
}

func traceSampler() sdktrace.Sampler {
	if strings.EqualFold(strings.TrimSpace(os.Getenv(otelSamplerEnvVar)), alwaysOnSampler) {
		return sdktrace.AlwaysSample()
	}

	return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(0.1))
}

type warnOnceExporter struct {
	sdktrace.SpanExporter

	once sync.Once
}

func (e *warnOnceExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	err := e.SpanExporter.ExportSpans(ctx, spans)
	if err != nil {
		e.once.Do(func() {
			slog.Warn("OpenTelemetry export failed", "error", err)
		})
	}

	return err
}
