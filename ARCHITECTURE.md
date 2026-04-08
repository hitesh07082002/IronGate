# IronGate — Architecture Reference

> Runtime reference for the shipped implementation.
>
> Start with [`README.md`](./README.md) for the overview and demo path. Use [`PROJECT_SPEC.md`](./PROJECT_SPEC.md) for full scope and [`DESIGN_DOC.md`](./DESIGN_DOC.md) for target-state design. If those documents disagree with the code, this file is the source of truth for current behavior.
>
> Phase 9 Milestones 1 through 4 are now part of the shipped runtime. The remaining Phase 9 planning docs under [`docs/phase9-planning/`](./docs/phase9-planning/) describe future work only. This file wins whenever planning docs disagree with current runtime behavior.

---

## How To Use This Document

This file covers:

- the live middleware and transport ordering
- the shipped config contract and runtime behavior
- the current request flow, headers, routes, and verification commands

For broader product scope and future work, use:

- [`DESIGN_DOC.md`](./DESIGN_DOC.md) for target architecture, algorithms, and tradeoffs
- [`PROJECT_SPEC.md`](./PROJECT_SPEC.md) for full feature scope and project requirements
- [`PROGRESS.md`](./PROGRESS.md) for what is shipped now versus planned next
- [`docs/phase9-planning/`](./docs/phase9-planning/) for the remaining Chaos Observatory milestones beyond shipped M4

---

## 1. Shipped Snapshot

### What Is Shipped

- Reverse proxy gateway built with `net/http` and `httputil.ReverseProxy`
- Outer middleware chain: `Tracing -> Router -> Metrics -> Auth -> RateLimiter -> Proxy`
- Inner transport chain: `Retry -> LoadBalancer -> CircuitBreaker -> BaseTransport`
- OpenTelemetry root and middleware spans when `OTEL_EXPORTER_OTLP_ENDPOINT` is configured
- W3C `traceparent` propagation to upstream services plus trace-linked request duration exemplars on OpenMetrics scrapes
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
- Optional admin server serving `POST /admin/circuit-breakers/reset` when `ADMIN_TOKEN` is set; defaults to `127.0.0.1:9090` unless `ADMIN_ADDR` overrides it
- Graceful shutdown that flips `/ready` to `503` before draining in-flight requests
- Direct `/metrics` Prometheus handler with service-only labels
- `gateway_circuit_state{service}` gauge showing `0=CLOSED`, `1=OPEN`, `2=HALF_OPEN` for the current per-service breaker aggregate
- Gateway-exposed payment routes: `POST /api/payments` for creation and `GET /api/payments/{id}` for status lookup
- `make load-test` backed by [`benchmarks/smoke.js`](./benchmarks/smoke.js)
- `make benchmark` backed by [`benchmarks/scenarios.json`](./benchmarks/scenarios.json), [`benchmarks/route.js`](./benchmarks/route.js), and [`benchmarks/runner.py`](./benchmarks/runner.py)
- `make benchmark-test` backed by [`benchmarks/test_runner.py`](./benchmarks/test_runner.py) to keep the benchmark artifact contract regression-tested
- Recorded benchmark bundle under [`benchmarks/results/20260406-033854-d1edb38/`](./benchmarks/results/20260406-033854-d1edb38/README.md)
- Top-level [`README.md`](./README.md) with quick start, architecture diagram, benchmark summary, and doc links
- [`demo.sh`](./demo.sh) for an end-to-end local stack smoke run
- [`scripts/capture-demo.sh`](./scripts/capture-demo.sh) plus [`artifacts/demo/README.md`](./artifacts/demo/README.md) for regenerating the 2-minute demo asset without committing a large binary
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
- Optional observatory overlay via [`docker-compose.observatory.yml`](./docker-compose.observatory.yml) adding Tempo, the OTel Collector, Toxiproxy, the local observatory API on `127.0.0.1:9000`, and gateway OTel/admin environment wiring
- React frontend under [`web/`](./web/) served by the observatory overlay on `127.0.0.1:3001`, with same-origin `/api` proxying through nginx to the observatory API

The codebase still contains some future-facing config fields so later phases can plug into the same route model. In the current implementation, unsupported later-phase features such as non-sliding-window rate limiting still fail closed instead of being silently ignored.

---

## 2. Current Project Structure

```text
irongate/
├── README.md
├── artifacts/
│   └── demo/
│       └── README.md
├── cmd/
│   ├── observatory/
│   │   ├── api.go
│   │   ├── app.go
│   │   ├── chaos.go
│   │   ├── events.go
│   │   ├── main.go
│   │   ├── metrics.go
│   │   ├── reset.go
│   │   ├── runner.go
│   │   ├── scenarios.go
│   │   └── toxiproxy.go
│   └── gateway/
│       ├── admin_test.go
│       ├── main.go
│       ├── main_test.go
│       └── phase7_test.go
├── benchmarks/
│   ├── results/
│   │   ├── 20260406-033854-d1edb38/
│   │   └── README.md
│   ├── route.js
│   ├── runner.py
│   ├── scenarios.json
│   ├── test_runner.py
│   └── smoke.js
├── configs/
│   └── gateway.yaml
├── demo.sh
├── Dockerfile.observatory
├── internal/
│   ├── config/
│   │   ├── config.go
│   │   └── config_test.go
│   ├── metrics/
│   │   ├── registry.go
│   │   └── registry_test.go
│   ├── middleware/
│   │   ├── chain.go
│   │   ├── auth.go
│   │   ├── auth_test.go
│   │   ├── metrics.go
│   │   ├── observability_test.go
│   │   ├── ratelimit.go
│   │   ├── ratelimit_test.go
│   │   ├── router.go
│   │   ├── router_tracing_test.go
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
│   ├── telemetry/
│   │   ├── events.go
│   │   ├── telemetry.go
│   │   └── telemetry_test.go
│   ├── testutil/
│   │   └── redis.go
│   └── transport/
│       ├── attempt.go
│       ├── doc.go
│       ├── errors.go
│       ├── observability_test.go
│       ├── resilient.go
│       ├── resilient_otel_test.go
│       ├── resilient_test.go
│       ├── retry.go
│       ├── retry_test.go
│       ├── circuitbreaker/
│       │   ├── breaker.go
│       │   ├── breaker_test.go
│       │   └── registry.go
│       └── loadbalancer/
│           ├── balancer.go
│           ├── least_conn.go
│           ├── loadbalancer_test.go
│           ├── round_robin.go
│           └── weighted.go
├── services/
│   ├── common/
│   │   ├── chaos.go
│   │   ├── health.go
│   │   └── mock.go
│   ├── user-service/
│   ├── order-service/
│   └── payment-service/
├── docker-compose.observatory.yml
├── docker-compose.yml
├── monitoring/
│   ├── grafana/
│   └── prometheus/
├── otel/
│   ├── collector-config.yaml
│   └── tempo.yaml
├── scenarios/
│   ├── auth-wall.yaml
│   ├── cascading-failure.yaml
│   ├── circuit-breaker-recovery.yaml
│   ├── happy-path.yaml
│   ├── latency-injection.yaml
│   ├── rate-limit-storm.yaml
│   ├── redis-impaired.yaml
│   ├── single-replica-death.yaml
│   ├── upstream-5xx-retry.yaml
│   └── k6/
├── ADR/
├── DESIGN_DOC.md
├── PROJECT_SPEC.md
├── PROGRESS.md
└── scripts/
    └── capture-demo.sh
```

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

Implemented in [`internal/middleware/tracing.go`](./internal/middleware/tracing.go).

- Strips incoming `X-Request-ID`, `X-User-ID`, and `X-User-Role`
- Always generates a fresh UUID request ID
- Writes the generated `X-Request-ID` to both the upstream request and client response
- Starts the root `irongate.request` span plus a tracing middleware child span
- Injects W3C trace headers into the upstream request via the global OTel propagator
- Logs request start and completion with status and latency

This is a deliberate sanitization boundary. Client-supplied request IDs are not trusted in the current runtime.

The direct `/health`, `/ready`, and `/metrics` handlers also strip `X-Request-ID`, `X-User-ID`, and `X-User-Role`, then issue a fresh gateway request ID before responding.

### Admin Control Plane

When `ADMIN_TOKEN` is set, `main` starts a second HTTP server for observatory-only administrative actions.

- `POST /admin/circuit-breakers/reset`
- Requires `Authorization: Bearer $ADMIN_TOKEN`
- Uses constant-time token comparison
- Calls `Registry.Reset()` on the active circuit-breaker registry and returns `{"reset":true,"targets_cleared":N}`

This server is separate from the public gateway listener on `:8080`. The default bind address is `127.0.0.1:9090`; the observatory overlay sets `ADMIN_ADDR=:9090` so the admin plane stays reachable inside Docker without publishing a host port.

### Observatory Server

Phase 9 Milestones 2 and 3 add a separate observatory process under [`cmd/observatory`](./cmd/observatory) that binds to `127.0.0.1:9000` in the overlay and orchestrates the demo backend.

- `GET /api/health` reports aggregate status (`ok` or `degraded`), spec version, validated demo JWT readiness, Toxiproxy readiness, plus ordered per-service health for gateway, Redis, and the demo replicas used in the UI status rail
- `GET /api/scenarios`, `GET /api/scenarios/statuses`, `GET /api/scenarios/{name}`, and `GET /api/scenarios/{name}/status` expose the built-in scenario catalog
- `POST /api/scenarios/{name}/run` and `POST /api/scenarios/{name}/stop` require `Authorization: Bearer $DEMO_TOKEN`
- `GET /api/events` streams gateway and system events over SSE for the demo UI
- `GET /api/metrics/query` and `GET /api/metrics/query_range` proxy a restricted subset of Prometheus queries
- `GET /api/dashboard/landing` and `GET /api/dashboard/chaos` batch the shipped frontend's Prometheus reads so the UI stays within the observatory rate-limit contract
- `POST /api/reset` restores service health, clears Toxiproxy toxics, stops managed k6 containers, and resets gateway circuit breakers

The observatory runner starts short-lived `grafana/k6` containers against the Docker network, passes scenario intensity into each run, uses Toxiproxy for Redis impairment scenarios, and relies on structured gateway event logs from [`internal/telemetry/events.go`](./internal/telemetry/events.go) for the SSE stream. The shipped catalog currently includes nine scenarios covering healthy traffic, auth rejection, rate limiting, single-target failure, retry absorption, circuit-breaker recovery, cascading failure, Redis impairment, and latency injection.

### Observatory Frontend

Phase 9 Milestone 4 adds a Vite/React/Tailwind frontend under [`web/`](./web/) with:

- `/` landing page showing the animated gateway pipeline and live counter strip
- `/about` for the problem framing, clickable pipeline nodes, and ADR decision cards
- `/chaos` for the three-column observatory surface with scenario controls, live SSE feed, Prometheus-backed charts, and recent trace bar
- `/observability` for Grafana metrics iframes, Tempo explore embedding, and filtered SSE log playback

The `web` image is built in [`web/Dockerfile`](./web/Dockerfile), served by nginx on `127.0.0.1:3001`, and proxies `/api/*` to the observatory container so browser clients never need CORS exceptions for local demo runs. The observatory overlay also enables Grafana anonymous viewer access for demo-only iframe embedding on `/observability`.

#### `Router`

Implemented in [`internal/middleware/router.go`](./internal/middleware/router.go).

- Sorts routes by descending path length at startup
- Uses longest-prefix matching
- Enforces route method allowlists
- Returns `404` for unknown routes
- Returns `405` plus `Allow` for disallowed methods
- Stores the matched `RouteConfig` in request context

#### `Auth`

Implemented in [`internal/middleware/auth.go`](./internal/middleware/auth.go). The demo user service in [`services/user-service/main.go`](./services/user-service/main.go) can also accept benchmark-only `subject` and `role` overrides on `/users/login` when `IRONGATE_ALLOW_LOGIN_OVERRIDES=true`; the default runtime keeps that override path disabled.

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

Implemented in [`internal/middleware/ratelimit.go`](./internal/middleware/ratelimit.go) and [`internal/ratelimit/store.go`](./internal/ratelimit/store.go).

- Reads the matched `RouteConfig` from context
- Skips routes with `rate_limit: null`
- Supports `sliding_window` only in the current runtime
- Uses authenticated `X-User-ID` when present
- Falls back to client IP for unauthenticated routes
- Trusts `X-Forwarded-For` only for explicitly wired trusted proxy IPs
- Defaults to trusting no proxies on the runtime path
- Parses the optional `IRONGATE_TRUSTED_PROXIES` env var in [`cmd/gateway/main.go`](./cmd/gateway/main.go) so the benchmark stack can emulate many public clients from one host without weakening the default runtime contract
- Uses a Redis Lua script plus sorted sets for atomic sliding-window enforcement
- Keys counters as `rate_limit:{client_key}:{route.Path}`
- Uses the gateway-generated `X-Request-ID` as the Redis sorted-set member
- Sets `X-RateLimit-Limit`, `X-RateLimit-Remaining`, and `X-RateLimit-Reset`
- Returns `429 Too Many Requests` with `Retry-After` when the route is over quota
- Fails open when Redis is unavailable and omits authoritative rate-limit headers in that case

#### `UnsupportedFeatures`

- No longer participates in the live handler chain
- Retained only as a compatibility shim for legacy references and tests

#### `Proxy`

Implemented in [`internal/proxy/proxy.go`](./internal/proxy/proxy.go).

- Still handles `gateway-internal` routes defensively if they reach the proxy, but `/health` and `/ready` are intercepted earlier by the runtime manager
- Applies per-route timeout with fallback to server `WriteTimeout`
- Delegates all upstream selection to the transport layer
- Uses `httputil.ReverseProxy` `Rewrite`, not `Director`
- Maps `transport.ErrCircuitOpen` and `transport.ErrNoHealthyTargets` to standardized `503` JSON responses
- Preserves `504` for upstream deadline expiry

---

## 4. Current Transport Pipeline

`transport.NewResilientTransport(nil, cfg.Routes, cfg.CircuitBreaker, metricsRegistry, breakerRegistry)` currently returns:

```text
[RetryTransport] -> [LoadBalancerTransport] -> [CircuitBreakerTransport] -> [Base http.Transport]
```

Retry owns the per-attempt loop, load balancer target selection, and circuit-breaker fail-fast behavior are explicit transport layers now.

### Retry Transport

Implemented in [`internal/transport/retry.go`](./internal/transport/retry.go).

- Reads per-route retry config from `RouteConfig` in context
- Retries only idempotent methods by default: `GET`, `HEAD`, `PUT`, `DELETE`, `OPTIONS`
- Retries only `502`, `503`, `504`, plus transient connection and timeout errors
- Applies exponential backoff with full jitter
- Replays buffered request bodies for retried requests with bodies
- Carries retry count plus already-tried targets in request context so downstream layers can act on per-attempt metadata
- Treats open circuits as fail-fast target exclusions, not backoff retries

### Load Balancer Transport

Implemented in [`internal/transport/resilient.go`](./internal/transport/resilient.go).

- Reads the matched `RouteConfig` from context
- Selects a target inside the transport layer, not in the proxy director path
- Clones the request before mutating upstream URL/host
- Sets `X-Served-By` from the actual selected upstream host
- Sets `X-Retry-Count` and `X-Retry-Target` from the final attempt metadata
- Prefers a different target on retry attempts when alternatives exist
- Releases least-connection counters when the response body is fully read or closed

### Circuit Breaker Transport

Implemented in [`internal/transport/resilient.go`](./internal/transport/resilient.go) plus the breaker state machine in [`internal/transport/circuitbreaker/`](./internal/transport/circuitbreaker/).

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

That means the current implementation forwards:

- `X-Forwarded-For`
- `X-Forwarded-Host`
- `X-Forwarded-Proto`
- `X-Request-ID`

The upstream `Host` header is the selected upstream instance, not the original client host. The original client host is carried in `X-Forwarded-Host`.

---

## 6. Config Contract

The `Config` and `RouteConfig` structs in [`internal/config/config.go`](./internal/config/config.go) already include some future-phase fields. That is intentional.

### Default shipped route fields

The checked-in [`configs/gateway.yaml`](./configs/gateway.yaml) only uses fields supported in the current runtime:

- `path`
- `strip_prefix`
- `service`
- `methods`
- `auth_required`
- `timeout`
- `rate_limit`
- `targets`
- `load_balancer`

The checked-in config also declares the gateway-internal `/health` and `/ready` routes for completeness. Those paths are reserved for the gateway: validation rejects upstream services or auth/rate-limit settings there, and the runtime manager serves them directly before proxying logic is reached.

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

These config fields are live in the current runtime:

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

Rules in the current runtime:

- Keeping these fields in the struct is allowed
- Retry, circuit-breaker, and metrics settings are runtime-supported and validated on load
- Logging config is still parsed but inert until a later phase lands
- `gateway-internal` routes may omit targets

---

## 7. Error and Header Rules

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

## 8. Observability

- `/metrics` is served directly by [`internal/runtime/manager.go`](./internal/runtime/manager.go), not by the service middleware chain.
- The metrics endpoint strips `X-User-ID`, `X-User-Role`, and `X-Request-ID` before handling the request and only serves loopback or private-network clients.
- Every gateway-exported application metric uses only the `{service}` label.
- Exported application series in the current runtime:
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

Verification commands:

```bash
make lint
make build
IRONGATE_TEST_REDIS_ADDR=127.0.0.1:6379 make test
IRONGATE_TEST_REDIS_ADDR=127.0.0.1:6379 make coverage
IRONGATE_TEST_REDIS_ADDR=127.0.0.1:6379 make test-race
make benchmark-test
make load-test
make benchmark
```

`make test`, `make coverage`, and `make test-race` require a running Redis instance when you want the Redis-backed integration tests to execute locally. Without `IRONGATE_TEST_REDIS_ADDR`, those Redis integration tests are skipped.

`make coverage` enforces a repo-wide statement coverage floor of 70%.
`make benchmark-test` exercises the Python benchmark runner without requiring a live stack.
Run `mise install` once in the repo root to install the pinned `k6` toolchain used by the benchmark commands.
`make load-test` requires `k6` plus a reachable gateway, defaulting to `http://127.0.0.1:8080`.
`make benchmark` uses the same `k6` setup, boots the benchmark contract from [`benchmarks/scenarios.json`](./benchmarks/scenarios.json), and records machine-readable bundles under [`benchmarks/results/`](./benchmarks/results/README.md).
[`demo.sh`](./demo.sh) boots the local Compose stack, waits for `/ready`, exercises protected routes, samples `/metrics`, and then runs the k6 smoke test.
[`scripts/capture-demo.sh`](./scripts/capture-demo.sh) always captures the demo transcript and optionally records an MP4 on macOS when `ffmpeg` plus an `avfoundation` `IRONGATE_CAPTURE_SOURCE` are configured; [`artifacts/demo/README.md`](./artifacts/demo/README.md) covers Linux and Windows alternatives.

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
Outer: [Tracing] -> [Router] -> [Metrics] -> [Auth] -> [RateLimiter] -> [Proxy]
Inner: [Retry] -> [LoadBalancer] -> [CircuitBreaker] -> [BaseTransport]
```

Treat that ordering as the current runtime. Later planned extensions should preserve the core request/transport split unless the runtime contract is intentionally revised.
