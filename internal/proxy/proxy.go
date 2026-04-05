package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"github.com/hitesh07082002/irongate/internal/middleware"
	"github.com/hitesh07082002/irongate/internal/response"
	"github.com/hitesh07082002/irongate/internal/transport"
)

const (
	gatewayInternalService = "gateway-internal"
	fallbackRouteTimeout   = 30 * time.Second
)

type Handler struct {
	logger         *slog.Logger
	proxy          *httputil.ReverseProxy
	defaultTimeout time.Duration
}

func New(logger *slog.Logger, defaultTimeout time.Duration, upstreamTransport http.RoundTripper) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if defaultTimeout <= 0 {
		defaultTimeout = fallbackRouteTimeout
	}
	if upstreamTransport == nil {
		upstreamTransport = http.DefaultTransport
	}

	reverseProxy := &httputil.ReverseProxy{
		Rewrite: func(proxyReq *httputil.ProxyRequest) {
			route := middleware.GetRouteConfig(proxyReq.In)
			if route == nil {
				return
			}

			proxyReq.Out.URL.Scheme = "http"
			proxyReq.Out.URL.Path = stripPrefix(route.StripPrefix, proxyReq.Out.URL.Path)
			if proxyReq.Out.URL.RawPath != "" {
				proxyReq.Out.URL.RawPath = stripPrefix(route.StripPrefix, proxyReq.Out.URL.RawPath)
			}
			proxyReq.SetXForwarded()
		},
		Transport: upstreamTransport,
		ModifyResponse: func(resp *http.Response) error {
			if resp != nil && resp.Request != nil && resp.Header.Get(transport.HeaderServedBy) == "" {
				resp.Header.Set(transport.HeaderServedBy, resp.Request.URL.Host)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			statusCode := http.StatusBadGateway
			message := "upstream request failed"
			switch {
			case errors.Is(err, context.DeadlineExceeded), errors.Is(req.Context().Err(), context.DeadlineExceeded):
				statusCode = http.StatusGatewayTimeout
				message = "upstream request timed out"
			case errors.Is(err, transport.ErrNoHealthyTargets):
				statusCode = http.StatusServiceUnavailable
				message = noHealthyTargetsMessage(req)
			case errors.Is(err, transport.ErrCircuitOpen):
				statusCode = http.StatusServiceUnavailable
				message = "upstream circuit is open"
			}

			logger.Error("proxy error",
				"method", req.Method,
				"path", req.URL.Path,
				"request_id", response.RequestID(req),
				"error", err,
			)
			transport.ApplyErrorHeaders(w.Header(), err)
			response.WriteError(w, req, statusCode, message)
		},
	}

	return &Handler{
		logger:         logger,
		proxy:          reverseProxy,
		defaultTimeout: defaultTimeout,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	route := middleware.GetRouteConfig(req)
	if route == nil {
		response.WriteError(w, req, http.StatusInternalServerError, "route config missing")
		return
	}

	if route.Service == gatewayInternalService {
		response.WriteJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"service": "gateway",
		})
		return
	}

	if len(route.Targets) == 0 {
		response.WriteError(w, req, http.StatusBadGateway, "no upstream targets configured")
		return
	}

	routeTimeout := route.Timeout
	if routeTimeout <= 0 {
		routeTimeout = h.defaultTimeout
	}

	ctx, cancel := context.WithTimeout(req.Context(), routeTimeout)
	defer cancel()
	req = req.WithContext(ctx)

	h.proxy.ServeHTTP(w, req)
}

func stripPrefix(prefix, path string) string {
	normalizedPrefix := strings.TrimSuffix(prefix, "/")
	if normalizedPrefix == "" {
		normalizedPrefix = prefix
	}
	if normalizedPrefix == "" {
		return path
	}
	if path == normalizedPrefix {
		return "/"
	}
	if !strings.HasPrefix(path, normalizedPrefix+"/") {
		return path
	}

	trimmed := strings.TrimPrefix(path, normalizedPrefix)
	if trimmed == "" {
		return "/"
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "/" + trimmed
	}

	return trimmed
}

func noHealthyTargetsMessage(req *http.Request) string {
	route := middleware.GetRouteConfig(req)
	if route == nil || route.Service == "" {
		return "no healthy targets available"
	}

	return fmt.Sprintf("no healthy targets for service: %s", route.Service)
}
