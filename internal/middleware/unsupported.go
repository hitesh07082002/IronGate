package middleware

import (
	"net/http"

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

			next.ServeHTTP(w, req)
		})
	}
}
