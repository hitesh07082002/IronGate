package middleware

import (
	"net/http"
	"time"

	gatewaymetrics "github.com/hitesh07082002/irongate/internal/metrics"
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
