# IronGate — Implementation Reference

> This is the implementation reference for the current `main` branch.
>
> Project status: in progress. `main` has shipped Phase 1 foundation, Phase 2 load balancing, Phase 3 JWT authentication, Phase 4 Redis-backed rate limiting, Phase 5 retry plus circuit breaking, Phase 6 observability, and Phase 7 production-readiness runtime management. Later documentation and benchmark work remains planned.
>
> For target end-state scope and design, see [`PROJECT_SPEC.md`](./PROJECT_SPEC.md) and [`DESIGN_DOC.md`](./DESIGN_DOC.md). If either conflicts with this file, this file wins for the current runtime.

---

## Current Runtime

This file documents the architecture that is actually shipped on `main` today:

- live middleware and transport ordering
- runtime-reference config contract and supported behavior
- current headers, routes, and verification coverage

## Full Project Target Design

The complete end-state and future-phase architecture lives in:

- [`DESIGN_DOC.md`](./DESIGN_DOC.md) for target architecture, algorithms, and tradeoffs
- [`PROJECT_SPEC.md`](./PROJECT_SPEC.md) for full feature scope and project requirements
- [`PROGRESS.md`](./PROGRESS.md) for what is shipped now versus planned next

---

## 1. Current Main Snapshot

### Shipped on `main`

- Reverse proxy gateway built with `net/http` and `httputil.ReverseProxy`
- Outer middleware chain: `Tracing -> Router -> Auth -> RateLimiter -> Proxy`
- Inner transport chain: `Retry -> LoadBalancer -> CircuitBreaker -> BaseTransport`
- Load-balancing strategies:
  - `round_robin` via atomic counter
  - `weighted` via smooth weighted round robin
  - `least_conn` via in-memory atomic counters
- Longest-prefix routing plus per-route method allowlists
- Per-route timeout handling in the proxy
- `X-Forwarded-*` propagation through proxy rewrite
- `X-Served-By` reporting the actual selected upstream
- `X-Retry-Count` and `X-Retry-Target` reporting retry outcomes
- Atomic runtime snapshot manager backed by `atomic.Pointer[*runtime.Snapshot]`
- fsnotify-based config hot reload with debounce and fail-safe rollback to the last valid snapshot
- Direct gateway-served `/health`, `/ready`, and `/metrics` handling
- Graceful shutdown that flips `/ready` to `503` before draining in-flight requests
- Direct `/metrics` Prometheus handler with service-only labels
- Gateway-exposed payment routes: `POST /api/payments` for creation and `GET /api/payments/{id}` for status lookup
- `make load-test` backed by [`benchmarks/smoke.js`](./benchmarks/smoke.js)
- [`demo.sh`](./demo.sh) for an end-to-end local stack smoke run
- Docker Compose with:
  - `gateway`
  - `redis`
  - `prometheus`
  - `grafana`
  - `user-service-1`, `user-service-2`
  - `order-service-1`, `order-service-2`
  - `payment-service-1`
  - shared `JWT_SECRET` provided to the gateway and both user-service instances at startup
  - `GRAFANA_ADMIN_USER` and `GRAFANA_ADMIN_PASSWORD` provided to Grafana at startup
  - Redis kept internal-only on the Compose network
  - Prometheus and Grafana bound to `127.0.0.1` on the host for local-only access

The codebase still contains some future-facing config fields so later phases can plug into the same route model. On `main`, unsupported later-phase features such as non-sliding-window rate limiting still fail closed instead of being silently ignored.

---

## 2. Current Project Structure

```text
irongate/
├── cmd/
│   └── gateway/
│       ├── main.go
│       ├── main_test.go
│       └── phase7_test.go
├── benchmarks/
│   └── smoke.js
├── configs/
│   └── gateway.yaml
├── demo.sh
├── internal/
│   ├── config/
│   │   ├── config.go
│   │   └── config_test.go
│   ├── middleware/
│   │   ├── chain.go
│   │   ├── auth.go
│   │   ├── auth_test.go
│   │   ├── ratelimit.go
│   │   ├── ratelimit_test.go
│   │   ├── router.go
│   │   ├── tracing.go
│   │   └── unsupported.go
│   ├── proxy/
│   │   └── proxy.go
│   ├── ratelimit/
│   │   ├── store.go
│   │   └── store_test.go
│   ├── response/
│   │   └── response.go
│   ├── runtime/
│   │   ├── manager.go
│   │   └── watcher.go
│   ├── testutil/
│   │   └── redis.go
│   └── transport/
│       ├── doc.go
│       ├── resilient.go
│       └── loadbalancer/
│           ├── balancer.go
│           ├── round_robin.go
│           ├── weighted.go
│           ├── least_conn.go
│           └── loadbalancer_test.go
├── services/
│   ├── common/
│   │   ├── chaos.go
│   │   ├── health.go
│   │   └── mock.go
│   ├── user-service/
│   ├── order-service/
│   └── payment-service/
├── docker-compose.yml
├── ADR/
├── DESIGN_DOC.md
├── PROJECT_SPEC.md
└── PROGRESS.md
```

Only list files here that exist on `main`.

---

## 3. Current Request Pipeline

### Outer Chain

`newRuntimeManager` in [`cmd/gateway/main.go`](./cmd/gateway/main.go) builds a [`runtime.Manager`](./internal/runtime/manager.go) that serves:

- `/health` directly as a liveness check
- `/ready` directly as a readiness/drain check (`200` only when a valid snapshot is loaded and shutdown has not begun)
- the current snapshot's `metrics.path` directly, with the existing internal-only guard
- all other paths through the current runtime snapshot's application handler

Each incoming request loads the current snapshot once from the atomic pointer. In-flight requests keep the snapshot they started with even if a reload swaps in a newer one.

Each snapshot wires the application chain as:

```go
return middleware.Chain(
    proxyHandler,
    middleware.Tracing(logger),
    middleware.Router(cfg.Routes),
    middleware.Metrics(metricsRegistry),
    middleware.Auth(cfg.Auth),
    middleware.RateLimiterWithMetrics(rateLimitStore, logger, metricsRegistry, ...),
)
```

Because `Chain` applies middleware in reverse order, service traffic still flows as:

```text
Service Request -> [Tracing] -> [Router] -> [Metrics] -> [Auth] -> [RateLimiter] -> [Proxy] -> Response
```

Direct internal paths bypass the service middleware chain entirely.

#### `Tracing`

- Strips incoming `X-Request-ID`, `X-User-ID`, and `X-User-Role`
- Always generates a fresh UUID request ID
- Writes the generated `X-Request-ID` to both the upstream request and client response
- Logs request start and completion with status and latency

This is a deliberate sanitization boundary. Client-supplied request IDs are not trusted on `main`.

The direct `/health`, `/ready`, and `/metrics` handlers also strip `X-Request-ID`, `X-User-ID`, and `X-User-Role`, then issue a fresh gateway request ID before responding.

#### `Router`

- Sorts routes by descending path length at startup
- Uses longest-prefix matching
- Enforces route method allowlists
- Returns `404` for unknown routes
- Returns `405` plus `Allow` for disallowed methods
- Stores the matched `RouteConfig` in request context

#### `Auth`

- Reads the matched `RouteConfig` from context
- Skips routes with `auth_required: false`
- Requires `Authorization: Bearer <token>` on protected routes
- Explicitly enforces `HS256` from config
- Verifies signature with the configured shared secret
- Validates `exp` and `iat`
- Injects `X-User-ID` from JWT `sub`
- Injects `X-User-Role` from JWT `role`
- Removes the original bearer `Authorization` header before proxying protected requests downstream
- Fails closed with `500` if JWT auth is misconfigured

#### `RateLimiter`

- Reads the matched `RouteConfig` from context
- Skips routes with `rate_limit: null`
- Supports `sliding_window` only on `main`
- Uses authenticated `X-User-ID` when present
- Falls back to client IP for unauthenticated routes
- Trusts `X-Forwarded-For` only for explicitly wired trusted proxy IPs
- Defaults to trusting no proxies on `main`
- Uses a Redis Lua script plus sorted sets for atomic sliding-window enforcement
- Keys counters as `rate_limit:{client_key}:{route.Path}`
- Uses the gateway-generated `X-Request-ID` as the Redis sorted-set member
- Sets `X-RateLimit-Limit`, `X-RateLimit-Remaining`, and `X-RateLimit-Reset`
- Returns `429 Too Many Requests` with `Retry-After` when the route is over quota
- Fails open when Redis is unavailable and omits authoritative rate-limit headers in that case

#### `UnsupportedFeatures`

- No longer participates in the live Phase 5 handler chain
- Retained only as a compatibility shim for legacy references and tests

#### `Proxy`

- Still handles `gateway-internal` routes defensively if they reach the proxy, but `/health` and `/ready` are intercepted earlier by the runtime manager
- Applies per-route timeout with fallback to server `WriteTimeout`
- Delegates all upstream selection to the transport layer
- Uses `httputil.ReverseProxy` `Rewrite`, not `Director`
- Maps `transport.ErrCircuitOpen` and `transport.ErrNoHealthyTargets` to standardized `503` JSON responses
- Preserves `504` for upstream deadline expiry

---

## 4. Current Transport Pipeline

`transport.NewResilientTransport(nil, cfg.CircuitBreaker)` currently returns:

```text
[RetryTransport] -> [LoadBalancerTransport] -> [CircuitBreakerTransport] -> [Base http.Transport]
```

Retry owns the per-attempt loop, load balancer target selection, and circuit-breaker fail-fast behavior are explicit transport layers now.

### Retry Transport

- Reads per-route retry config from `RouteConfig` in context
- Retries only idempotent methods by default: `GET`, `HEAD`, `PUT`, `DELETE`, `OPTIONS`
- Retries only `502`, `503`, `504`, plus transient connection and timeout errors
- Applies exponential backoff with full jitter
- Replays buffered request bodies for retried requests with bodies
- Carries retry count plus already-tried targets in request context so downstream layers can act on per-attempt metadata
- Treats open circuits as fail-fast target exclusions, not backoff retries

### Load Balancer Transport

- Reads the matched `RouteConfig` from context
- Selects a target inside the transport layer, not in the proxy director path
- Clones the request before mutating upstream URL/host
- Sets `X-Served-By` from the actual selected upstream host
- Sets `X-Retry-Count` and `X-Retry-Target` from the final attempt metadata
- Prefers a different target on retry attempts when alternatives exist
- Releases least-connection counters when the response body is fully read or closed

### Circuit Breaker Transport

- Maintains a concurrent-safe per-target (`host:port`) breaker registry
- Counts only `5xx` responses plus connection and timeout failures toward opening a circuit
- Supports `CLOSED -> OPEN -> HALF-OPEN -> CLOSED`
- Returns `transport.ErrCircuitOpen` for open targets so retry can fail over or surface `no healthy targets`

### Base Transport

Current base transport settings in [`internal/transport/resilient.go`](./internal/transport/resilient.go):

- `MaxIdleConnsPerHost = 100`
- `MaxConnsPerHost = 100`
- `IdleConnTimeout = 90s`

### Balancer Implementations

#### Round Robin

- Uses `atomic.Uint64`
- Selection index is `(counter - 1) % len(targets)`

#### Weighted

- Uses smooth weighted round robin
- Defaults missing or non-positive weights to `1`

#### Least Connections

- Uses per-target `atomic.Int64` active counters
- Increments on selection
- Decrements through a `Done()` callback guarded by `sync.Once`

---

## 5. Proxy Rewriting and Forwarded Headers

The proxy in [`internal/proxy/proxy.go`](./internal/proxy/proxy.go):

- rewrites the upstream scheme to `http`
- strips `route.StripPrefix` from `URL.Path` and `URL.RawPath`
- calls `ProxyRequest.SetXForwarded()`

That means `main` now forwards:

- `X-Forwarded-For`
- `X-Forwarded-Host`
- `X-Forwarded-Proto`
- `X-Request-ID`

The upstream `Host` header is the selected upstream instance, not the original client host. The original client host is carried in `X-Forwarded-Host`.

---

## 6. Config Contract on `main`

The `Config` and `RouteConfig` structs in [`internal/config/config.go`](./internal/config/config.go) already include some future-phase fields. That is intentional.

### Default shipped route fields

The checked-in [`configs/gateway.yaml`](./configs/gateway.yaml) only uses fields supported on `main`:

- `path`
- `strip_prefix`
- `service`
- `methods`
- `auth_required`
- `timeout`
- `rate_limit`
- `targets`
- `load_balancer`

The checked-in config also declares the gateway-internal `/health` and `/ready` routes for completeness, even though the runtime manager serves those paths directly before proxying logic is reached.

### Default shipped top-level config fields

The checked-in config also actively uses:

- `server`
- `routes`
- `auth`
- `redis`

The checked-in [`configs/gateway.yaml`](./configs/gateway.yaml) expects `JWT_SECRET` from the
environment and validates `jwt_algorithm: HS256` when any route requires auth. The
checked-in Compose stack also expects `GRAFANA_ADMIN_USER` and `GRAFANA_ADMIN_PASSWORD`
for the local Grafana instance.

### Runtime-supported live fields

These config fields are live on `main` today:

- route-level `retry`
- top-level `circuit_breaker`
- top-level `metrics`
- runtime hot reload for routes, auth, Redis, circuit breaker, and metrics settings

### Startup-only server fields

These settings are validated on reload but do not change the live `http.Server` after startup:

- `server.port`
- `server.read_timeout`
- `server.write_timeout`

### Future fields already parsed

These fields exist in config structs today but are not live features yet:

- `logging`

Rules on `main`:

- Keeping these fields in the struct is allowed
- Retry, circuit-breaker, and metrics settings are runtime-supported and validated on load
- Logging config is still parsed but inert until a later phase lands
- `gateway-internal` routes may omit targets

---

## 7. Error and Header Rules on `main`

### Standard error body

Gateway-generated errors use:

```json
{
  "error": "human-readable message",
  "code": 404,
  "request_id": "uuid"
}
```

Implemented middleware and proxy paths should preserve this format.

### Sanitized incoming headers

The gateway strips these incoming client headers before request processing:

- `X-Request-ID`
- `X-User-ID`
- `X-User-Role`

### Upstream visibility

Upstreams currently see:

- a fresh gateway-generated `X-Request-ID`
- `X-Forwarded-*` headers from the proxy
- `X-User-ID` and `X-User-Role` on authenticated routes
- no forwarded `Authorization` header on protected routes after gateway auth succeeds
- `X-Served-By` on the response back to the client

## 8. Observability on `main`

- `/metrics` is mounted directly in [`cmd/gateway/main.go`](./cmd/gateway/main.go). It does not flow through router auth, rate limiting, or proxy logic.
- The metrics endpoint strips `X-User-ID`, `X-User-Role`, and `X-Request-ID` before handling the request and only serves loopback or private-network clients.
- Every gateway-exported application metric uses only the `{service}` label.
- Exported application series on `main`:
  - `gateway_requests_total`
  - `gateway_request_failures_total`
  - `gateway_request_duration_seconds`
  - `gateway_rate_limit_rejections_total`
  - `gateway_retries_total`
  - `gateway_retry_delay_seconds`
  - `gateway_circuit_opens_total`
  - `gateway_open_circuits`
  - `gateway_upstream_duration_seconds`
  - `gateway_in_flight_requests`

---

## 9. Verification

Current repo verification commands:

```bash
make lint
IRONGATE_TEST_REDIS_ADDR=127.0.0.1:6379 make test
IRONGATE_TEST_REDIS_ADDR=127.0.0.1:6379 make coverage
IRONGATE_TEST_REDIS_ADDR=127.0.0.1:6379 make test-race
make build
make load-test
```

`make test`, `make coverage`, and `make test-race` require a running Redis instance when you want the Redis-backed integration tests to execute locally. Without `IRONGATE_TEST_REDIS_ADDR`, those Redis integration tests are skipped.

`make coverage` enforces a repo-wide statement coverage floor of 70%.
`make load-test` requires `k6` plus a reachable gateway, defaulting to `http://127.0.0.1:8080`.
[`demo.sh`](./demo.sh) boots the local Compose stack, waits for `/ready`, exercises protected routes, samples `/metrics`, and then runs the k6 smoke test.

Key test coverage lives in:

- [`cmd/gateway/main_test.go`](./cmd/gateway/main_test.go)
- [`internal/config/config_test.go`](./internal/config/config_test.go)
- [`internal/middleware/auth_test.go`](./internal/middleware/auth_test.go)
- [`internal/middleware/ratelimit_test.go`](./internal/middleware/ratelimit_test.go)
- [`internal/ratelimit/store_test.go`](./internal/ratelimit/store_test.go)
- [`internal/transport/loadbalancer/loadbalancer_test.go`](./internal/transport/loadbalancer/loadbalancer_test.go)
- [`services/user-service/main_test.go`](./services/user-service/main_test.go)

---

## 10. Planned Extensions

The live runtime still uses the same architectural split:

- outer `http.Handler` middleware for request-level concerns
- inner `http.RoundTripper` layers for transport-level concerns

Current steady-state order:

```text
Outer: [Tracing] -> [Router] -> [Auth] -> [RateLimiter] -> [Proxy]
Inner: [Retry] -> [LoadBalancer] -> [CircuitBreaker] -> [BaseTransport]
```

Treat that ordering as the current runtime. Later phases add observability and operational tooling without changing the core request/transport split.
