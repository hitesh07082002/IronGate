package middleware

import (
	"net/http"
	"time"

	gatewaymetrics "github.com/hitesh07082002/irongate/internal/metrics"
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
				registry.ObserveRequest(route.Service, recorder.statusCode, time.Since(start))
			}()

			next.ServeHTTP(recorder, req)
		})
	}
}
