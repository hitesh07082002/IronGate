package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"github.com/hitesh07082002/irongate/internal/config"
	gatewaymetrics "github.com/hitesh07082002/irongate/internal/metrics"
	"github.com/hitesh07082002/irongate/internal/middleware"
	"github.com/hitesh07082002/irongate/internal/ratelimit"
	"github.com/hitesh07082002/irongate/internal/response"
	gwruntime "github.com/hitesh07082002/irongate/internal/runtime"
	"github.com/hitesh07082002/irongate/internal/telemetry"
	"github.com/hitesh07082002/irongate/internal/transport/circuitbreaker"
)

const (
	fallbackShutdownTimeout = 10 * time.Second
	trustedProxiesEnvVar    = "IRONGATE_TRUSTED_PROXIES"
	adminAddrEnvVar         = "ADMIN_ADDR"
	defaultAdminAddr        = "127.0.0.1:9090"
)

type buildHandlerOptions struct {
	rateLimitStore  ratelimit.Store
	trustedProxies  []netip.Prefix
	metricsRegistry *gatewaymetrics.Registry
	tracerProvider  trace.TracerProvider
}

type serverError struct {
	name string
	err  error
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	configPathFlag := flag.String("config", "", "path to the gateway config file")
	flag.Parse()

	configPath := resolveConfigPath(*configPathFlag)
	cfg, err := config.Load(configPath)
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

	trustedProxies, err := resolveTrustedProxies()
	if err != nil {
		slog.Error("failed to parse trusted proxies", "error", err)
		os.Exit(1)
	}

	tp, shutdownTracing := telemetry.Init(context.Background(), "irongate", "phase9")
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTracing(shutdownCtx)
	}()

	manager, err := newRuntimeManager(cfg, logger, buildHandlerOptions{
		trustedProxies: trustedProxies,
		tracerProvider: tp,
	})
	if err != nil {
		slog.Error("failed to build runtime manager", "error", err)
		os.Exit(1)
	}

	adminToken := strings.TrimSpace(os.Getenv("ADMIN_TOKEN"))
	cbGetter := func() *circuitbreaker.Registry {
		snapshot := manager.Current()
		if snapshot == nil {
			return nil
		}

		return snapshot.CircuitBreaker
	}

	var adminServer *http.Server
	if adminToken != "" {
		adminServer = &http.Server{
			Addr:         resolveAdminAddr(),
			Handler:      newAdminHandler(adminToken, cbGetter),
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		}
	}
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      manager,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	slog.Info("gateway started", "port", cfg.Server.Port, "routes", len(cfg.Routes))
	watcher, err := gwruntime.NewWatcher(configPath, manager, logger, 0)
	if err != nil {
		slog.Error("failed to initialize config watcher", "error", err)
		os.Exit(1)
	}

	watcherCtx, cancelWatcher := context.WithCancel(context.Background())
	defer cancelWatcher()
	go func() {
		if err := watcher.Run(watcherCtx); err != nil {
			logger.Error("config watcher stopped", "error", err)
		}
	}()

	serverErrors := make(chan serverError, 2)
	if adminServer != nil {
		serveAsync("admin", adminServer, serverErrors)
	}
	serveAsync("gateway", server, serverErrors)

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	select {
	case serverErr := <-serverErrors:
		cancelWatcher()
		manager.BeginShutdown()
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout(cfg.Server.WriteTimeout))
		defer cancelShutdown()
		if adminServer != nil {
			_ = adminServer.Shutdown(shutdownCtx)
		}
		_ = server.Shutdown(shutdownCtx)
		slog.Error(serverErr.name+" server stopped", "error", serverErr.err)
		os.Exit(1)
	case <-signalCtx.Done():
	}

	manager.BeginShutdown()
	cancelWatcher()

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout(cfg.Server.WriteTimeout))
	defer cancelShutdown()
	if adminServer != nil {
		if err := adminServer.Shutdown(shutdownCtx); err != nil {
			slog.Error("admin graceful shutdown failed", "error", err)
			os.Exit(1)
		}
	}
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
}

func buildHandler(cfg *config.Config, logger *slog.Logger) http.Handler {
	return buildHandlerWithOptions(cfg, logger, buildHandlerOptions{})
}

func buildHandlerWithOptions(cfg *config.Config, logger *slog.Logger, options buildHandlerOptions) http.Handler {
	manager, err := newRuntimeManager(cfg, logger, options)
	if err != nil {
		panic(err)
	}

	return manager
}

func newRuntimeManager(cfg *config.Config, logger *slog.Logger, options buildHandlerOptions) (*gwruntime.Manager, error) {
	managerConfig := cfg
	if options.rateLimitStore != nil && cfg != nil && strings.TrimSpace(cfg.Redis.Address) == "" && hasRateLimitedRoutes(cfg.Routes) {
		managerConfig = cfg.Clone()
		managerConfig.Redis.Address = "runtime-store-injected"
	}

	return gwruntime.NewManager(managerConfig, gwruntime.BuilderOptions{
		Logger:          logger,
		TrustedProxies:  options.trustedProxies,
		MetricsRegistry: options.metricsRegistry,
		TracerProvider:  options.tracerProvider,
		RateLimitStoreFactory: func(next *config.Config, previous *gwruntime.Snapshot) ratelimit.Store {
			if options.rateLimitStore != nil {
				return options.rateLimitStore
			}
			if next == nil || !hasRateLimitedRoutes(next.Routes) {
				return nil
			}
			if previous != nil && previous.Config != nil && previous.RateLimitStore != nil && previous.Config.Redis == next.Redis {
				return previous.RateLimitStore
			}

			return ratelimit.NewRedisStore(next.Redis)
		},
	})
}

func hasRateLimitedRoutes(routes []config.RouteConfig) bool {
	for _, route := range routes {
		if route.RateLimit != nil {
			return true
		}
	}

	return false
}

func shutdownTimeout(writeTimeout time.Duration) time.Duration {
	if writeTimeout > 0 {
		return writeTimeout
	}

	return fallbackShutdownTimeout
}

func resolveTrustedProxies() ([]netip.Prefix, error) {
	rawValue := strings.TrimSpace(os.Getenv(trustedProxiesEnvVar))
	if rawValue == "" {
		return nil, nil
	}

	parts := strings.Split(rawValue, ",")
	prefixes := make([]netip.Prefix, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}

		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("%s contains invalid prefix %q: %w", trustedProxiesEnvVar, value, err)
		}

		masked := prefix.Masked()
		key := masked.String()
		if _, ok := seen[key]; ok {
			continue
		}

		prefixes = append(prefixes, masked)
		seen[key] = struct{}{}
	}

	return prefixes, nil
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

func resolveAdminAddr() string {
	if value := strings.TrimSpace(os.Getenv(adminAddrEnvVar)); value != "" {
		return value
	}

	return defaultAdminAddr
}

func newAdminHandler(adminToken string, cbGetter func() *circuitbreaker.Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = stampAdminRequest(w, r)
		if r.URL.Path != "/admin/circuit-breakers/reset" {
			response.WriteError(w, r, http.StatusNotFound, "not found")
			return
		}
		if r.Method != http.MethodPost {
			response.WriteError(w, r, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		auth, ok := adminBearerToken(r.Header.Get("Authorization"))
		if !ok || !adminTokenMatches(auth, adminToken) {
			response.WriteError(w, r, http.StatusUnauthorized, "unauthorized")
			return
		}

		n := 0
		if cbGetter != nil {
			if registry := cbGetter(); registry != nil {
				n = registry.Reset()
			}
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"reset":true,"targets_cleared":%d}`, n)
	})
}

func serveAsync(name string, server *http.Server, errs chan<- serverError) {
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- serverError{name: name, err: err}
		}
	}()
}

func adminBearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || scheme != "Bearer" {
		return "", false
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return "", false
	}

	return token, true
}

func adminTokenMatches(provided, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return false
	}

	providedHash := sha256.Sum256([]byte(provided))
	expectedHash := sha256.Sum256([]byte(expected))
	return hmac.Equal(providedHash[:], expectedHash[:])
}

func stampAdminRequest(w http.ResponseWriter, r *http.Request) *http.Request {
	if r == nil {
		return nil
	}
	if r.Header == nil {
		r.Header = make(http.Header)
	}

	r.Header.Del(middleware.HeaderRequestID)
	r.Header.Del(middleware.HeaderUserID)
	r.Header.Del(middleware.HeaderUserRole)

	requestID := uuid.NewString()
	r.Header.Set(middleware.HeaderRequestID, requestID)
	if w != nil {
		w.Header().Set(middleware.HeaderRequestID, requestID)
	}

	return r
}
