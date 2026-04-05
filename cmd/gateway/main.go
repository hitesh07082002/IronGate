package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"

	"github.com/google/uuid"

	"github.com/hitesh07082002/irongate/internal/config"
	gatewaymetrics "github.com/hitesh07082002/irongate/internal/metrics"
	"github.com/hitesh07082002/irongate/internal/middleware"
	"github.com/hitesh07082002/irongate/internal/proxy"
	"github.com/hitesh07082002/irongate/internal/ratelimit"
	"github.com/hitesh07082002/irongate/internal/response"
	"github.com/hitesh07082002/irongate/internal/transport"
)

const metricsInternalOnlyMessage = "metrics endpoint is internal only"

type buildHandlerOptions struct {
	rateLimitStore  ratelimit.Store
	trustedProxies  []netip.Prefix
	metricsRegistry *gatewaymetrics.Registry
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	configPathFlag := flag.String("config", "", "path to the gateway config file")
	flag.Parse()

	cfg, err := config.Load(resolveConfigPath(*configPathFlag))
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	if validationErrors := cfg.Validate(); len(validationErrors) > 0 {
		for _, validationErr := range validationErrors {
			slog.Error("config validation failed", "error", validationErr)
		}
		os.Exit(1)
	}

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      buildHandler(cfg, logger),
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	slog.Info("gateway started", "port", cfg.Server.Port, "routes", len(cfg.Routes))
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}

func buildHandler(cfg *config.Config, logger *slog.Logger) http.Handler {
	return buildHandlerWithOptions(cfg, logger, buildHandlerOptions{})
}

func buildHandlerWithOptions(cfg *config.Config, logger *slog.Logger, options buildHandlerOptions) http.Handler {
	rateLimitStore := options.rateLimitStore
	if rateLimitStore == nil && hasRateLimitedRoutes(cfg.Routes) {
		rateLimitStore = ratelimit.NewRedisStore(cfg.Redis)
	}

	metricsRegistry := options.metricsRegistry
	if !cfg.Metrics.Enabled {
		metricsRegistry = nil
	} else if metricsRegistry == nil {
		metricsRegistry = gatewaymetrics.NewRegistry()
	}

	proxyHandler := proxy.New(logger, cfg.Server.WriteTimeout, transport.NewResilientTransport(nil, cfg.Routes, cfg.CircuitBreaker, metricsRegistry))
	applicationHandler := middleware.Chain(
		proxyHandler,
		middleware.Tracing(logger),
		middleware.Router(cfg.Routes),
		middleware.Metrics(metricsRegistry),
		middleware.Auth(cfg.Auth),
		middleware.RateLimiterWithMetrics(rateLimitStore, logger, metricsRegistry, middleware.RateLimiterOptions{
			TrustedProxies: options.trustedProxies,
		}),
	)

	if !cfg.Metrics.Enabled || metricsRegistry == nil {
		return applicationHandler
	}

	mux := http.NewServeMux()
	mux.Handle(cfg.Metrics.Path, metricsHandler(metricsRegistry.Handler()))
	mux.Handle("/", applicationHandler)
	return mux
}

func metricsHandler(next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.Header.Del(middleware.HeaderUserID)
		req.Header.Del(middleware.HeaderUserRole)
		req.Header.Del(middleware.HeaderRequestID)
		requestID := uuid.NewString()
		req.Header.Set(middleware.HeaderRequestID, requestID)
		w.Header().Set(middleware.HeaderRequestID, requestID)

		if !isInternalMetricsClient(req.RemoteAddr) {
			response.WriteError(w, req, http.StatusForbidden, metricsInternalOnlyMessage)
			return
		}

		next.ServeHTTP(w, req)
	})
}

func isInternalMetricsClient(remoteAddr string) bool {
	addr, ok := parseRemoteAddr(remoteAddr)
	if !ok {
		return false
	}

	return addr.IsLoopback() || addr.IsPrivate()
}

func parseRemoteAddr(remoteAddr string) (netip.Addr, bool) {
	trimmed := strings.TrimSpace(remoteAddr)
	if trimmed == "" {
		return netip.Addr{}, false
	}

	if host, _, err := net.SplitHostPort(trimmed); err == nil {
		trimmed = host
	}

	addr, err := netip.ParseAddr(trimmed)
	if err != nil {
		return netip.Addr{}, false
	}

	return addr, true
}

func hasRateLimitedRoutes(routes []config.RouteConfig) bool {
	for _, route := range routes {
		if route.RateLimit != nil {
			return true
		}
	}

	return false
}

func resolveConfigPath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if envPath := os.Getenv("IRONGATE_CONFIG"); envPath != "" {
		return envPath
	}
	if envPath := os.Getenv("GATEWAY_CONFIG"); envPath != "" {
		return envPath
	}

	return "configs/gateway.yaml"
}
