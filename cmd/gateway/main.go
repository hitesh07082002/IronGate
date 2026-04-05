package main

import (
	"context"
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

	"github.com/hitesh07082002/irongate/internal/config"
	gatewaymetrics "github.com/hitesh07082002/irongate/internal/metrics"
	"github.com/hitesh07082002/irongate/internal/ratelimit"
	gwruntime "github.com/hitesh07082002/irongate/internal/runtime"
)

const fallbackShutdownTimeout = 10 * time.Second

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

	manager, err := newRuntimeManager(cfg, logger, buildHandlerOptions{})
	if err != nil {
		slog.Error("failed to build runtime manager", "error", err)
		os.Exit(1)
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

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	select {
	case err := <-serverErrors:
		cancelWatcher()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("gateway stopped", "error", err)
			os.Exit(1)
		}
		return
	case <-signalCtx.Done():
	}

	manager.BeginShutdown()
	cancelWatcher()

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout(cfg.Server.WriteTimeout))
	defer cancelShutdown()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}

	if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("gateway stopped", "error", err)
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
