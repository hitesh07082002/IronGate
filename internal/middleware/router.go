package middleware

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/hitesh07082002/irongate/internal/response"
)

type contextKey string

const RouteConfigKey contextKey = "routeConfig"

type router struct {
	routes []config.RouteConfig
}

func Router(routes []config.RouteConfig) Middleware {
	sortedRoutes := append([]config.RouteConfig(nil), routes...)
	sort.SliceStable(sortedRoutes, func(left, right int) bool {
		return len(sortedRoutes[left].Path) > len(sortedRoutes[right].Path)
	})

	r := &router{routes: sortedRoutes}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			route := r.match(req.URL.Path)
			if route == nil {
				response.WriteError(w, req, http.StatusNotFound, "route not found")
				return
			}
			if !methodAllowed(route.Methods, req.Method) {
				w.Header().Set("Allow", strings.Join(route.Methods, ", "))
				response.WriteError(w, req, http.StatusMethodNotAllowed, "method not allowed")
				return
			}

			ctx := context.WithValue(req.Context(), RouteConfigKey, route)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	}
}

func GetRouteConfig(req *http.Request) *config.RouteConfig {
	cfg, _ := req.Context().Value(RouteConfigKey).(*config.RouteConfig)
	return cfg
}

func (r *router) match(path string) *config.RouteConfig {
	for index := range r.routes {
		route := &r.routes[index]
		if strings.HasPrefix(path, route.Path) {
			return route
		}
	}

	return nil
}

func methodAllowed(allowedMethods []string, method string) bool {
	if len(allowedMethods) == 0 {
		return true
	}

	for _, allowedMethod := range allowedMethods {
		if strings.EqualFold(allowedMethod, method) {
			return true
		}
	}

	return false
}
