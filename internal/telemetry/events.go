package telemetry

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"
)

var jwtLikePattern = regexp.MustCompile(`^eyJ[A-Za-z0-9_+=/-]*\.[A-Za-z0-9_+=/-]*\.[A-Za-z0-9_+=/-]*$`)

var (
	cachedDemoToken  = strings.TrimSpace(os.Getenv("DEMO_TOKEN"))
	cachedAdminToken = strings.TrimSpace(os.Getenv("ADMIN_TOKEN"))
)

func LogGatewayEvent(logger *slog.Logger, level slog.Level, typ, message string, attrs map[string]any) {
	if strings.TrimSpace(typ) == "" {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}

	record := slog.NewRecord(time.Now(), level, message, 0)
	record.AddAttrs(
		slog.String("type", strings.TrimSpace(typ)),
		slog.Any("attrs", sanitizeEventAttrs(attrs)),
	)
	_ = logger.Handler().Handle(context.Background(), record)
}

func TraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}

	spanCtx := trace.SpanContextFromContext(ctx)
	if !spanCtx.IsValid() {
		return ""
	}

	return spanCtx.TraceID().String()
}

func sanitizeEventAttrs(attrs map[string]any) map[string]any {
	if len(attrs) == 0 {
		return map[string]any{}
	}

	sanitized := make(map[string]any, len(attrs))
	for key, value := range attrs {
		sanitized[key] = sanitizeEventValue(value)
	}

	return sanitized
}

func sanitizeEventValue(value any) any {
	switch typed := value.(type) {
	case string:
		return sanitizeEventString(typed)
	case []string:
		out := make([]string, len(typed))
		for index, entry := range typed {
			out[index] = sanitizeEventString(entry)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, entry := range typed {
			out[index] = sanitizeEventValue(entry)
		}
		return out
	case map[string]any:
		return sanitizeEventAttrs(typed)
	default:
		return value
	}
}

func sanitizeEventString(value string) string {
	trimmed := strings.TrimSpace(value)
	switch {
	case trimmed == "":
		return value
	case jwtLikePattern.MatchString(trimmed):
		return "[redacted-jwt]"
	case eventSecretMatch(trimmed):
		return "[redacted-secret]"
	default:
		return value
	}
}

func eventSecretMatch(value string) bool {
	if cachedDemoToken != "" && subtleStringMatch(value, cachedDemoToken) {
		return true
	}

	return cachedAdminToken != "" && subtleStringMatch(value, cachedAdminToken)
}

func subtleStringMatch(left, right string) bool {
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
