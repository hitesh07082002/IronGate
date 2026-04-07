package runtime

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"

	"github.com/hitesh07082002/irongate/internal/config"
	gatewaymetrics "github.com/hitesh07082002/irongate/internal/metrics"
	"github.com/hitesh07082002/irongate/internal/middleware"
	"github.com/hitesh07082002/irongate/internal/proxy"
	"github.com/hitesh07082002/irongate/internal/ratelimit"
	"github.com/hitesh07082002/irongate/internal/response"
	"github.com/hitesh07082002/irongate/internal/telemetry"
	"github.com/hitesh07082002/irongate/internal/transport"
	"github.com/hitesh07082002/irongate/internal/transport/circuitbreaker"
)

const (
	HealthPath                 = "/health"
	ReadyPath                  = "/ready"
	MetricsInternalOnlyMessage = "metrics endpoint is internal only"
	gatewayNotReadyMessage     = "gateway runtime is not ready"
	gatewayDrainingMessage     = "gateway is draining"
)

type RateLimitStoreFactory func(*config.Config, *Snapshot) ratelimit.Store

type BuilderOptions struct {
	Logger                *slog.Logger
	TrustedProxies        []netip.Prefix
	MetricsRegistry       *gatewaymetrics.Registry
	RateLimitStoreFactory RateLimitStoreFactory
	TracerProvider        trace.TracerProvider
}

type Snapshot struct {
	Config         *config.Config
	Application    http.Handler
	MetricsEnabled bool
	MetricsPath    string
	RateLimitStore ratelimit.Store
	CircuitBreaker *circuitbreaker.Registry
}

type Manager struct {
	logger               *slog.Logger
	metricsRegistry      *gatewaymetrics.Registry
	metricsHandler       http.Handler
	startupServer        config.ServerConfig
	trustedProxies       []netip.Prefix
	rateLimitStoreFactor RateLimitStoreFactory
	tracerProvider       trace.TracerProvider

	current  atomic.Pointer[Snapshot]
	ready    atomic.Bool
	draining atomic.Bool
}

func NewManager(initial *config.Config, options BuilderOptions) (*Manager, error) {
	if initial == nil {
		return nil, errors.New("runtime manager requires an initial config")
	}

	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}

	metricsRegistry := options.MetricsRegistry
	if metricsRegistry == nil {
		metricsRegistry = gatewaymetrics.NewRegistry()
	}

	manager := &Manager{
		logger:               logger,
		metricsRegistry:      metricsRegistry,
		metricsHandler:       metricsRegistry.Handler(),
		startupServer:        initial.Server,
		trustedProxies:       append([]netip.Prefix(nil), options.TrustedProxies...),
		rateLimitStoreFactor: options.RateLimitStoreFactory,
		tracerProvider:       options.TracerProvider,
	}
	if manager.rateLimitStoreFactor == nil {
		manager.rateLimitStoreFactor = defaultRateLimitStoreFactory
	}

	snapshot, err := manager.buildSnapshot(initial, nil)
	if err != nil {
		return nil, err
	}

	manager.current.Store(snapshot)
	manager.ready.Store(true)
	return manager, nil
}

func (m *Manager) Current() *Snapshot {
	if m == nil {
		return nil
	}

	return m.current.Load()
}

func (m *Manager) Ready() bool {
	if m == nil {
		return false
	}

	return m.ready.Load() && !m.draining.Load() && m.Current() != nil
}

func (m *Manager) BeginShutdown() {
	if m == nil {
		return
	}

	m.draining.Store(true)
	m.ready.Store(false)
}

func (m *Manager) Reload(next *config.Config) error {
	if m == nil {
		return errors.New("runtime manager is nil")
	}

	previous := m.Current()
	snapshot, err := m.buildSnapshot(next, previous)
	if err != nil {
		return err
	}

	m.current.Store(snapshot)
	if !m.draining.Load() {
		m.ready.Store(true)
	}

	m.logger.Info(
		"runtime snapshot swapped",
		"routes", len(snapshot.Config.Routes),
		"metrics_enabled", snapshot.MetricsEnabled,
		"metrics_path", snapshot.MetricsPath,
	)
	return nil
}

func (m *Manager) ReloadFromPath(path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}

	return m.Reload(cfg)
}

func (m *Manager) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch req.URL.Path {
	case HealthPath:
		m.serveHealth(w, req)
		return
	case ReadyPath:
		m.serveReady(w, req)
		return
	}

	snapshot := m.Current()
	if snapshot == nil {
		sanitizeGatewayRequest(w, req)
		response.WriteError(w, req, http.StatusServiceUnavailable, gatewayNotReadyMessage)
		return
	}

	if snapshot.MetricsEnabled && req.URL.Path == snapshot.MetricsPath {
		m.serveMetrics(w, req)
		return
	}

	snapshot.Application.ServeHTTP(w, req)
}

func (m *Manager) buildSnapshot(next *config.Config, previous *Snapshot) (*Snapshot, error) {
	if next == nil {
		return nil, errors.New("runtime snapshot requires config")
	}

	cfg := next.Clone()
	if validationErrors := cfg.Validate(); len(validationErrors) > 0 {
		return nil, errors.Join(validationErrors...)
	}
	if err := validateStartupOnlyServerConfig(m.startupServer, cfg.Server); err != nil {
		return nil, err
	}

	metricsRegistry := (*gatewaymetrics.Registry)(nil)
	metricsPath := ""
	if cfg.Metrics.Enabled {
		metricsRegistry = m.metricsRegistry
		metricsPath = strings.TrimSpace(cfg.Metrics.Path)
	}

	rateLimitStore := m.rateLimitStoreFactor(cfg, previous)
	breakerRegistry := nextCircuitBreakerRegistry(
		cfg.CircuitBreaker,
		circuitBreakerTargetServices(cfg.Routes),
		previous,
		m.metricsRegistry.RegisterCollector,
		m.metricsRegistry.UnregisterCollector,
	)
	tracingTracer := telemetry.TracerOrNoop(m.tracerProvider, "irongate.middleware.tracing")
	routerTracer := telemetry.TracerOrNoop(m.tracerProvider, "irongate.middleware.router")
	authTracer := telemetry.TracerOrNoop(m.tracerProvider, "irongate.middleware.auth")
	rateLimiterTracer := telemetry.TracerOrNoop(m.tracerProvider, "irongate.middleware.ratelimiter")
	transportTracer := telemetry.TracerOrNoop(m.tracerProvider, "irongate.transport")
	proxyHandler := proxy.New(
		m.logger,
		cfg.Server.WriteTimeout,
		transport.NewResilientTransport(nil, cfg.Routes, cfg.CircuitBreaker, metricsRegistry, breakerRegistry, transportTracer),
	)
	applicationHandler := middleware.Chain(
		proxyHandler,
		middleware.Tracing(m.logger, tracingTracer),
		middleware.Router(cfg.Routes, routerTracer),
		middleware.Metrics(metricsRegistry),
		middleware.Auth(cfg.Auth, authTracer),
		middleware.RateLimiterWithMetrics(rateLimitStore, m.logger, metricsRegistry, middleware.RateLimiterOptions{
			TrustedProxies: m.trustedProxies,
		}, rateLimiterTracer),
	)

	return &Snapshot{
		Config:         cfg,
		Application:    applicationHandler,
		MetricsEnabled: cfg.Metrics.Enabled && metricsRegistry != nil && metricsPath != "",
		MetricsPath:    metricsPath,
		RateLimitStore: rateLimitStore,
		CircuitBreaker: breakerRegistry,
	}, nil
}

func (m *Manager) serveHealth(w http.ResponseWriter, req *http.Request) {
	sanitizeGatewayRequest(w, req)
	response.WriteJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": "gateway",
	})
}

func (m *Manager) serveReady(w http.ResponseWriter, req *http.Request) {
	sanitizeGatewayRequest(w, req)
	if !m.Ready() {
		message := gatewayNotReadyMessage
		if m.draining.Load() {
			message = gatewayDrainingMessage
		}
		response.WriteError(w, req, http.StatusServiceUnavailable, message)
		return
	}

	response.WriteJSON(w, http.StatusOK, map[string]string{
		"status":  "ready",
		"service": "gateway",
	})
}

func (m *Manager) serveMetrics(w http.ResponseWriter, req *http.Request) {
	sanitizeGatewayRequest(w, req)
	if !isInternalMetricsClient(req.RemoteAddr) {
		response.WriteError(w, req, http.StatusForbidden, MetricsInternalOnlyMessage)
		return
	}

	m.metricsHandler.ServeHTTP(w, req)
}

func defaultRateLimitStoreFactory(cfg *config.Config, previous *Snapshot) ratelimit.Store {
	if cfg == nil || !hasRateLimitedRoutes(cfg.Routes) {
		return nil
	}
	if previous != nil && previous.Config != nil && previous.RateLimitStore != nil && previous.Config.Redis == cfg.Redis {
		return previous.RateLimitStore
	}

	return ratelimit.NewRedisStore(cfg.Redis)
}

func nextCircuitBreakerRegistry(
	cfg config.CBConfig,
	targetServices map[string]string,
	previous *Snapshot,
	registerCollector func(prometheus.Collector) error,
	unregisterCollector func(prometheus.Collector) bool,
) *circuitbreaker.Registry {
	if previous == nil || previous.CircuitBreaker == nil {
		registry := circuitbreaker.NewRegistry(cfg, registerCollector)
		registry.ReconcileTargetServices(targetServices)
		return registry
	}
	if previous.Config != nil && previous.Config.CircuitBreaker == cfg {
		previous.CircuitBreaker.ReconcileTargetServices(targetServices)
		return previous.CircuitBreaker
	}

	if unregisterCollector != nil {
		unregisterCollector(previous.CircuitBreaker.Collector())
	}

	cloned := previous.CircuitBreaker.CloneWithConfig(cfg, registerCollector)
	cloned.ReconcileTargetServices(targetServices)
	return cloned
}

func circuitBreakerTargetServices(routes []config.RouteConfig) map[string]string {
	targetServices := make(map[string]string)
	for _, route := range routes {
		for _, target := range route.Targets {
			targetServices[net.JoinHostPort(target.Host, strconv.Itoa(target.Port))] = route.Service
		}
	}

	return targetServices
}

func hasRateLimitedRoutes(routes []config.RouteConfig) bool {
	for _, route := range routes {
		if route.RateLimit != nil {
			return true
		}
	}

	return false
}

func validateStartupOnlyServerConfig(startup, next config.ServerConfig) error {
	var changed []string

	if startup.Port != next.Port {
		changed = append(changed, "server.port")
	}
	if startup.ReadTimeout != next.ReadTimeout {
		changed = append(changed, "server.read_timeout")
	}
	if startup.WriteTimeout != next.WriteTimeout {
		changed = append(changed, "server.write_timeout")
	}

	if len(changed) == 0 {
		return nil
	}

	return fmt.Errorf("%s are startup-only and cannot be hot reloaded", strings.Join(changed, ", "))
}

func sanitizeGatewayRequest(w http.ResponseWriter, req *http.Request) {
	if req.Header == nil {
		req.Header = make(http.Header)
	}

	req.Header.Del(middleware.HeaderUserID)
	req.Header.Del(middleware.HeaderUserRole)
	req.Header.Del(middleware.HeaderRequestID)

	requestID := uuid.NewString()
	req.Header.Set(middleware.HeaderRequestID, requestID)
	w.Header().Set(middleware.HeaderRequestID, requestID)
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
