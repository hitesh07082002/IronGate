package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"strconv"
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
	otlpEndpointEnvVar   = "OTEL_EXPORTER_OTLP_ENDPOINT"
	otlpInsecureEnvVar   = "OTEL_EXPORTER_OTLP_INSECURE"
	otelSamplerEnvVar    = "OTEL_TRACES_SAMPLER"
	otelSamplerArgEnvVar = "OTEL_TRACES_SAMPLER_ARG"

	alwaysOnSampler                = "always_on"
	alwaysOffSampler               = "always_off"
	traceIDRatioSampler            = "traceidratio"
	parentBasedAlwaysOnSampler     = "parentbased_always_on"
	parentBasedAlwaysOffSampler    = "parentbased_always_off"
	parentBasedTraceIDRatioSampler = "parentbased_traceidratio"

	otlpDialTimeout   = 5 * time.Second
	otlpExportTimeout = 10 * time.Second
)

func Init(ctx context.Context, serviceName, version string) (trace.TracerProvider, func(context.Context) error) {
	otel.SetTextMapPropagator(propagation.TraceContext{})

	endpoint := strings.TrimSpace(os.Getenv(otlpEndpointEnvVar))
	if endpoint == "" {
		return noop.NewTracerProvider(), func(context.Context) error { return nil }
	}
	if ctx == nil {
		ctx = context.Background()
	}

	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithDialOption(grpc.WithConnectParams(grpc.ConnectParams{
			MinConnectTimeout: otlpDialTimeout,
		})),
	}
	if otlpEndpointUsesURL(endpoint) {
		opts = append(opts, otlptracegrpc.WithEndpointURL(endpoint))
	} else {
		opts = append(opts, otlptracegrpc.WithEndpoint(endpoint))
	}
	if otlpInsecure() {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}

	exporter, err := otlptracegrpc.New(ctx, opts...)
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
	switch strings.ToLower(strings.TrimSpace(os.Getenv(otelSamplerEnvVar))) {
	case "":
		return defaultTraceSampler()
	case alwaysOnSampler:
		return sdktrace.AlwaysSample()
	case alwaysOffSampler:
		return sdktrace.NeverSample()
	case traceIDRatioSampler:
		return sdktrace.TraceIDRatioBased(traceSamplerRatio(1.0))
	case parentBasedAlwaysOnSampler:
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	case parentBasedAlwaysOffSampler:
		return sdktrace.ParentBased(sdktrace.NeverSample())
	case parentBasedTraceIDRatioSampler:
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(traceSamplerRatio(1.0)))
	default:
		return defaultTraceSampler()
	}
}

func otlpInsecure() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(otlpInsecureEnvVar)), "true")
}

func otlpEndpointUsesURL(endpoint string) bool {
	return strings.Contains(strings.TrimSpace(endpoint), "://")
}

func defaultTraceSampler() sdktrace.Sampler {
	return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(0.1))
}

func traceSamplerRatio(fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(otelSamplerArgEnvVar))
	if raw == "" {
		return fallback
	}

	ratio, err := strconv.ParseFloat(raw, 64)
	if err != nil || ratio < 0 || ratio > 1 {
		return fallback
	}

	return ratio
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
