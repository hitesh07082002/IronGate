package proxy

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"time"

	"github.com/hitesh07082002/irongate/internal/middleware"
	"github.com/hitesh07082002/irongate/internal/response"
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

func New(logger *slog.Logger, defaultTimeout time.Duration) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if defaultTimeout <= 0 {
		defaultTimeout = fallbackRouteTimeout
	}

	reverseProxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			route := middleware.GetRouteConfig(req)
			if route == nil || len(route.Targets) == 0 {
				return
			}

			target := route.Targets[0]
			req.URL.Scheme = "http"
			req.URL.Host = net.JoinHostPort(target.Host, strconv.Itoa(target.Port))
			req.Host = req.URL.Host
			req.URL.Path = stripPrefix(route.StripPrefix, req.URL.Path)
			if req.URL.RawPath != "" {
				req.URL.RawPath = stripPrefix(route.StripPrefix, req.URL.RawPath)
			}
		},
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 100,
			MaxConnsPerHost:     100,
			IdleConnTimeout:     90 * time.Second,
		},
		ModifyResponse: func(resp *http.Response) error {
			if resp != nil && resp.Request != nil {
				resp.Header.Set("X-Served-By", resp.Request.URL.Host)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			statusCode := http.StatusBadGateway
			message := "upstream request failed"
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(req.Context().Err(), context.DeadlineExceeded) {
				statusCode = http.StatusGatewayTimeout
				message = "upstream request timed out"
			}

			logger.Error("proxy error",
				"method", req.Method,
				"path", req.URL.Path,
				"request_id", response.RequestID(req),
				"error", err,
			)
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
	if prefix == "" || !strings.HasPrefix(path, prefix) {
		return path
	}

	trimmed := strings.TrimPrefix(path, prefix)
	if trimmed == "" {
		return "/"
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "/" + trimmed
	}

	return trimmed
}
