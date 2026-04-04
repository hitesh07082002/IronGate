# IronGate — Implementation Reference

> This is the implementation reference. AI coding tools (Codex, Claude Code) read this before writing code.
>
> For project scope and requirements, see [`PROJECT_SPEC.md`](./PROJECT_SPEC.md).
> For architecture rationale and algorithms, see [`DESIGN_DOC.md`](./DESIGN_DOC.md).

---

## 1. Project Structure

```
irongate/
├── cmd/
│   └── gateway/
│       └── main.go                  # Entry point: wires outer chain + inner transport
│
├── internal/
│   ├── config/
│   │   ├── config.go                # Config struct, YAML parsing, Validate()
│   │   └── watcher.go               # fsnotify hot-reload (Phase 7)
│   │
│   ├── middleware/                   # ── OUTER CHAIN (http.Handler) ──
│   │   ├── chain.go                 # func(http.Handler) http.Handler composition
│   │   ├── tracing.go               # X-Request-ID generation
│   │   ├── router.go                # Path match → store RouteConfig in context.Context
│   │   ├── auth/
│   │   │   ├── jwt.go               # JWT validation (reads auth_required from context)
│   │   │   └── claims.go            # Claims extraction
│   │   ├── ratelimit/
│   │   │   ├── ratelimit.go         # Middleware (reads rate config from context)
│   │   │   ├── sliding_window.go    # Sliding window core algorithm
│   │   │   └── redis_store.go       # Redis Lua script backend
│   │   └── metrics/
│   │       └── prometheus.go        # Prometheus metrics recording
│   │
│   ├── transport/                   # ── INNER CHAIN (http.RoundTripper) ──
│   │   ├── resilient.go             # Composes: retry → LB → CB → base transport
│   │   ├── retry.go                 # Backoff + jitter, clones req
│   │   ├── loadbalancer/
│   │   │   ├── balancer.go          # Interface
│   │   │   ├── round_robin.go
│   │   │   ├── weighted.go
│   │   │   └── least_conn.go        # In-memory atomic counter
│   │   └── circuitbreaker/
│   │       ├── breaker.go           # State machine (per-target, race-free)
│   │       └── registry.go          # Per-target breaker instances
│   │
│   └── proxy/
│       └── proxy.go                 # httputil.ReverseProxy with custom Transport
│
├── services/
│   ├── common/
│   │   ├── chaos.go                 # Shared chaos endpoints (latency, errors, down, reset)
│   │   ├── health.go                # Shared /health handler
│   │   └── mock.go                  # Generic mock CRUD generator
│   ├── user-service/
│   │   ├── main.go                  # ~30 lines, wires common + user-specific data
│   │   └── Dockerfile
│   ├── order-service/
│   │   ├── main.go
│   │   └── Dockerfile
│   └── payment-service/
│       ├── main.go
│       └── Dockerfile
│
├── configs/
│   ├── gateway.yaml                 # Main gateway config (see PROJECT_SPEC.md §5)
│   └── gateway.example.yaml         # Example for contributors
│
├── dashboards/
│   └── grafana-gateway.json         # Pre-built Grafana dashboard
│
├── scripts/
│   ├── generate-jwt.sh              # Helper to generate test JWTs
│   ├── load-test.js                 # k6 load test script
│   └── demo.sh                      # Automated demo script
│
├── benchmarks/
│   ├── results/                     # Benchmark data and graphs
│   └── k6/
│       ├── smoke.js
│       ├── load.js
│       └── stress.js
│
├── ADR/                             # Architecture Decision Records
│
├── docker-compose.yml
├── Dockerfile
├── go.mod
├── go.sum
├── Makefile
├── PROJECT_SPEC.md
├── DESIGN_DOC.md
├── ARCHITECTURE.md                  # (this file)
├── PROGRESS.md
└── README.md
```

The split is intentional: `middleware/` contains `http.Handler` wrappers (outer chain), `transport/` contains `http.RoundTripper` decorators (inner chain). Do not mix them.

---

## 2. Outer Chain: `http.Handler` Middleware

### Interface Pattern

Every outer middleware follows the standard Go pattern:

```go
type Middleware func(http.Handler) http.Handler
```

Applied in reverse order so the first-listed middleware is outermost:

```go
// In main.go or chain.go
handler := proxy                          // innermost
handler = rateLimitMiddleware(handler)
handler = authMiddleware(handler)
handler = routerMiddleware(handler)
handler = tracingMiddleware(handler)       // outermost
```

Resulting chain:
```
Request → [Tracing] → [Router] → [Auth] → [RateLimiter] → [Proxy] → Response
```

### Route Config via `context.Context`

Router resolves the matched route using **longest prefix match** (same as Nginx/Traefik). Routes are sorted by path length descending at startup. The first route whose path is a prefix of the request path wins. This means `/api/users/login` matches before `/api/users` for a request to `/api/users/login/callback`. Simple linear scan, good enough for <50 routes.

Router stores the matched `RouteConfig` in the request context:

```go
type contextKey string
const RouteConfigKey contextKey = "routeConfig"

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
    route := r.match(req.URL.Path)
    if route == nil {
        http.Error(w, "not found", 404)
        return
    }
    ctx := context.WithValue(req.Context(), RouteConfigKey, route)
    r.next.ServeHTTP(w, req.WithContext(ctx))
}

// Downstream middleware reads config from context
func getRouteConfig(req *http.Request) *RouteConfig {
    cfg, _ := req.Context().Value(RouteConfigKey).(*RouteConfig)
    return cfg
}
```

Every downstream middleware reads config from context, never from a global lookup. This is clean, testable, and thread-safe.

---

## 3. Inner Chain: `http.RoundTripper` Transport

### Interface

Every inner transport layer implements:

```go
type RoundTripper interface {
    RoundTrip(*http.Request) (*http.Response, error)
}
```

### Composition (`resilient.go`)

```go
func NewResilientTransport(
    baseTransport http.RoundTripper,
    lbFactory func(targets []Target) LoadBalancer,
    cbRegistry *circuitbreaker.Registry,
    retryConfig RetryConfig,
) http.RoundTripper {
    // Compose from inside out:

    // Circuit breaker wraps base transport
    cbTransport := &CircuitBreakerTransport{
        base:     baseTransport,
        registry: cbRegistry,
    }

    // Load balancer wraps circuit breaker
    lbTransport := &LoadBalancerTransport{
        next:      cbTransport,
        lbFactory: lbFactory,
    }

    // Retry wraps load balancer (outermost in inner chain)
    retryTransport := &RetryTransport{
        next:   lbTransport,
        config: retryConfig,
    }

    return retryTransport
}
```

---

## 4. Retry RoundTripper

```go
func (rt *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
    routeCfg := getRouteConfig(req)
    if !isRetryable(req.Method, routeCfg) {
        return rt.next.RoundTrip(req)
    }

    // Buffer body for retry (req.Clone doesn't clone the body)
    var bodyBytes []byte
    if req.Body != nil {
        bodyBytes, _ = io.ReadAll(req.Body)
        req.Body.Close()
    }

    var lastErr error
    var lastResp *http.Response

    for attempt := 0; attempt < routeCfg.Retry.MaxAttempts; attempt++ {
        if attempt > 0 {
            delay := computeBackoff(attempt, routeCfg.Retry)
            time.Sleep(delay)
        }

        // Clone request — RoundTrip must not mutate the original
        clone := req.Clone(req.Context())
        if bodyBytes != nil {
            clone.Body = io.NopCloser(bytes.NewReader(bodyBytes))
        }

        resp, err := rt.next.RoundTrip(clone)
        if err == nil && !isRetryableStatus(resp.StatusCode) {
            return resp, nil  // success
        }

        lastErr = err
        lastResp = resp
    }

    if lastResp != nil {
        return lastResp, nil
    }
    return nil, lastErr
}
```

### "No Healthy Targets" Path

```go
func (lb *LoadBalancerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
    target, err := lb.selectTarget(req)
    if err != nil {
        // All targets have open circuits or are unavailable
        return &http.Response{
            StatusCode: 503,
            Body:       io.NopCloser(strings.NewReader(
                `{"error": "no healthy targets for service: ` + serviceName + `"}`)),
        }, nil
    }
    req.URL.Host = target.Address()
    return lb.next.RoundTrip(req)
}
```

---

## 5. Config Struct & Validation

```go
type Config struct {
    Server         ServerConfig
    Routes         []RouteConfig
    CircuitBreaker CBConfig
    Auth           AuthConfig
    Redis          RedisConfig
    Metrics        MetricsConfig
    Logging        LoggingConfig
}

type RouteConfig struct {
    Path         string
    StripPrefix  string
    Service      string
    Methods      []string
    AuthRequired bool
    Timeout      time.Duration     // per-route timeout; 0 = use server defaults
    RateLimit    *RateLimitConfig  // nil = no rate limiting
    Retry        RetryConfig
    Targets      []Target
    LoadBalancer string            // "round_robin" | "weighted" | "least_conn"
}

type RateLimitConfig struct {
    Requests int           // max requests in window
    Window   time.Duration // sliding window duration
    Strategy string        // "sliding_window" (core), "token_bucket" (stretch)
}

type CBConfig struct {
    FailureThreshold    int           // failures within window to trip
    SuccessThreshold    int           // successes in half-open to close
    Timeout             time.Duration // OPEN → HALF-OPEN delay
    WindowSize          time.Duration // rolling failure count window
    HalfOpenMaxRequests int           // max probe requests in HALF-OPEN (default: 3)
}

func (c *Config) Validate() []error {
    // Collects ALL validation errors before returning.
    // Rejects: empty targets, unknown LB strategies, negative rate limits,
    // missing JWT secret, invalid durations, unknown strategies.
    // Startup prints all errors at once so the user fixes them in one pass.
}
```

**Config lifecycle:**
- Phase 1–6: Plain `Config` struct, passed at startup.
- Phase 7: Wrap in `atomic.Pointer[Config]` for lock-free hot-reload. Middleware already reads per-request config from context, so the refactor is minimal.

---

## 6. Connection Pool Tuning

Go's default `http.Transport` has `MaxIdleConnsPerHost=2`. Under load, this means the gateway creates and tears down TCP connections constantly, adding latency.

```go
transport := &http.Transport{
    MaxIdleConnsPerHost: 100,  // reuse connections (Go default: 2)
    MaxConnsPerHost:     100,  // cap total connections per target
    IdleConnTimeout:     90 * time.Second,
}
```

Set this from Phase 1. It's one line of config but the difference between 200 req/sec and 5000 req/sec.

---

## 7. Key Implementation Rules

1. **RoundTrip contract:** Go docs require `RoundTrip` must not mutate the request. Clone on every retry attempt with `req.Clone(req.Context())`.

2. **Circuit breaker scoping:** Per-target (`host:port`), not per-service. A failing instance must not take down healthy ones. Managed by `circuitbreaker.Registry`.

3. **Rate limiter client key:** Use authenticated user ID from JWT (`X-User-ID`). Fall back to client IP for unauthenticated routes. When fetching the client IP, use `X-Forwarded-For` ONLY if the TCP `RemoteAddr` matches a trusted proxy IP (e.g., Caddy or Cloudflare) to prevent IP spoofing limit bypasses.

4. **Fail-open:** Rate limiter allows requests through when Redis is down. Log warning. See [ADR-003](./ADR/003-fail-open-rate-limiting.md).

5. **Fail-closed:** Auth rejects all requests if JWT secret is misconfigured. This is a security boundary.

6. **Concurrent safety:** Circuit breaker state machine must pass `go test -race` with 100 goroutines hammering it simultaneously.

7. **Failure classification:** Only 5xx + connection errors count toward circuit breaker threshold. Never 4xx.

8. **Idempotent retries only:** Default retryable methods: GET, HEAD, PUT, DELETE, OPTIONS. POST is excluded to prevent duplicate resource creation.

9. **Per-route timeout:** Proxy applies `context.WithTimeout(req.Context(), route.Timeout)` before calling the inner transport. Default: 30s. This prevents a hanging upstream from accumulating goroutines.

10. **Metrics label cardinality:** Never use request path, user ID, or any unbounded value as a Prometheus label. Use `{service}` (from route config, bounded by config file). 10,000 unique paths = 10,000 time series = Prometheus OOM.

11. **Never buffer response bodies in middleware.** `httputil.ReverseProxy` streams responses by default. If any middleware reads `resp.Body`, it buffers the entire response in memory, killing throughput and risking OOM on large payloads. Metrics and logging should only touch headers and status codes, never the body.

12. **Header Sanitization:** The gateway MUST delete `X-User-ID`, `X-User-Role`, and `X-Request-ID` from all incoming requests immediately upon entry. Upstream services blindly trust these headers to determine authorization; if an attacker spoofs `X-User-ID` on a public route, they gain full access unless the gateway strips it first.

13. **Strict JWT Parsing:** When parsing JWTs, explicitly reject any token where `alg != HS256` in the token header, preventing `"none"` algorithm exploits or asymmetric-key confusion attacks.
