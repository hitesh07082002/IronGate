package middleware

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/hitesh07082002/irongate/internal/config"
	"github.com/hitesh07082002/irongate/internal/response"
)

type contextKey string

const RouteConfigKey contextKey = "routeConfig"
const routeCaptureKey contextKey = "routeCapture"

type routeCapture struct {
	route *config.RouteConfig
}

type router struct {
	routes []config.RouteConfig
}

func Router(routes []config.RouteConfig, tracer trace.Tracer) Middleware {
	if tracer == nil {
		tracer = noop.NewTracerProvider().Tracer("irongate.middleware.router")
	}

	sortedRoutes := append([]config.RouteConfig(nil), routes...)
	sort.SliceStable(sortedRoutes, func(left, right int) bool {
		return len(sortedRoutes[left].Path) > len(sortedRoutes[right].Path)
	})

	r := &router{routes: sortedRoutes}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			spanCtx, span := tracer.Start(req.Context(), "irongate.middleware.router")
			defer span.End()
			req = req.WithContext(spanCtx)

			route := r.match(req.URL.Path)
			if route == nil {
				span.SetAttributes(attribute.Bool("route.matched", false))
				span.SetStatus(codes.Error, "route not found")
				response.WriteError(w, req, http.StatusNotFound, "route not found")
				return
			}
			captureRoute(req, route)
			span.SetAttributes(
				attribute.String("route.service", route.Service),
				attribute.String("route.path", route.Path),
				attribute.Bool("route.matched", true),
			)
			if !methodAllowed(route.Methods, req.Method) {
				w.Header().Set("Allow", strings.Join(route.Methods, ", "))
				response.WriteError(w, req, http.StatusMethodNotAllowed, "method not allowed")
				return
			}

			ctx := context.WithValue(spanCtx, RouteConfigKey, route)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	}
}

func GetRouteConfig(req *http.Request) *config.RouteConfig {
	cfg, _ := req.Context().Value(RouteConfigKey).(*config.RouteConfig)
	return cfg
}

func withRouteCapture(ctx context.Context) (context.Context, *routeCapture) {
	capture := &routeCapture{}
	return context.WithValue(ctx, routeCaptureKey, capture), capture
}

func captureRoute(req *http.Request, route *config.RouteConfig) {
	if req == nil || route == nil {
		return
	}

	capture, _ := req.Context().Value(routeCaptureKey).(*routeCapture)
	if capture != nil {
		capture.route = route
	}
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
