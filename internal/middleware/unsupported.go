package middleware

import (
	"net/http"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/hitesh07082002/irongate/internal/response"
)

func UnsupportedFeatures() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			route := GetRouteConfig(req)
			if route == nil {
				response.WriteError(w, req, http.StatusInternalServerError, "route config missing")
				return
			}

			switch unsupportedFeature(route) {
			case "auth":
				response.WriteError(w, req, http.StatusNotImplemented, "route auth is not implemented yet")
				return
			case "rate_limit":
				response.WriteError(w, req, http.StatusNotImplemented, "route rate limiting is not implemented yet")
				return
			case "retry":
				response.WriteError(w, req, http.StatusNotImplemented, "route retries are not implemented yet")
				return
			}

			next.ServeHTTP(w, req)
		})
	}
}

func unsupportedFeature(route *config.RouteConfig) string {
	switch {
	case route.RateLimit != nil:
		return "rate_limit"
	case route.Retry.MaxAttempts > 1:
		return "retry"
	default:
		return ""
	}
}
