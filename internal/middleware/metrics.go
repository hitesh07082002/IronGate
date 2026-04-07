package middleware

import (
	"log/slog"
	"net/http"
	"time"

	gatewaymetrics "github.com/hitesh07082002/irongate/internal/metrics"
	"github.com/hitesh07082002/irongate/internal/response"
	"github.com/hitesh07082002/irongate/internal/telemetry"
	"go.opentelemetry.io/otel/trace"
)

func Metrics(registry *gatewaymetrics.Registry) Middleware {
	if registry == nil {
		return func(next http.Handler) http.Handler {
			return next
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			route := GetRouteConfig(req)
			if route == nil {
				next.ServeHTTP(w, req)
				return
			}

			recorder := &statusRecorder{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			registry.IncInFlight(route.Service)
			start := time.Now()
			defer func() {
				registry.DecInFlight(route.Service)
				spanCtx := trace.SpanFromContext(req.Context()).SpanContext()
				duration := time.Since(start)
				if recorder.statusCode < http.StatusBadRequest {
					telemetry.LogGatewayEvent(slog.Default(), slog.LevelInfo, "request_success", "request completed successfully", map[string]any{
						"service":    route.Service,
						"route":      route.Path,
						"status":     recorder.statusCode,
						"request_id": response.RequestID(req),
						"trace_id":   spanCtx.TraceID().String(),
					})
				}
				if spanCtx.IsValid() && spanCtx.IsSampled() {
					registry.ObserveRequestWithExemplar(
						route.Service,
						recorder.statusCode,
						duration,
						spanCtx.TraceID().String(),
					)
					return
				}

				registry.ObserveRequest(route.Service, recorder.statusCode, duration)
			}()

			next.ServeHTTP(recorder, req)
		})
	}
}
