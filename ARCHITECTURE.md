# IronGate — Implementation Reference

> This is the implementation reference for the current `main` branch.
>
> Project status: in progress. `main` has shipped Phase 1 foundation, Phase 2 load balancing, Phase 3 JWT authentication, and Phase 4 Redis-backed rate limiting. Later phases remain planned.
>
> For target end-state scope and design, see [`PROJECT_SPEC.md`](./PROJECT_SPEC.md) and [`DESIGN_DOC.md`](./DESIGN_DOC.md). If either conflicts with this file, this file wins for the current runtime.

---

## Current Runtime

This file documents the architecture that is actually shipped on `main` today:

- live middleware and transport ordering
- current config contract and fail-closed behavior
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
- Outer middleware chain: `Tracing -> Router -> Auth -> RateLimiter -> UnsupportedFeatures -> Proxy`
- Inner transport chain: `LoadBalancer -> BaseTransport`
- Load-balancing strategies:
  - `round_robin` via atomic counter
  - `weighted` via smooth weighted round robin
  - `least_conn` via in-memory atomic counters
- Longest-prefix routing plus per-route method allowlists
- Per-route timeout handling in the proxy
- `X-Forwarded-*` propagation through proxy rewrite
- `X-Served-By` reporting the actual selected upstream
- Gateway-served `/health` route via `gateway-internal`
- Gateway-exposed payment routes: `POST /api/payments` for creation and `GET /api/payments/{id}` for status lookup
- Docker Compose with:
  - `gateway`
  - `redis`
  - `user-service-1`, `user-service-2`
  - `order-service-1`, `order-service-2`
  - `payment-service-1`
  - shared `JWT_SECRET` provided to the gateway and both user-service instances at startup
  - Redis kept internal-only on the Compose network

### Planned, not shipped yet

- Retry transport
- Circuit breaker transport
- Metrics, Prometheus, and Grafana
- Config hot reload
- Graceful shutdown and readiness endpoints

The codebase already contains some future-facing config fields so later phases can plug into the same route model. On `main`, unsupported later-phase route features fail closed instead of being silently ignored.

---

## 2. Current Project Structure

```text
irongate/
├── cmd/
│   └── gateway/
│       ├── main.go
│       └── main_test.go
├── configs/
│   └── gateway.yaml
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

Only list files here that exist on `main`. Future files belong in the design docs until they are added to the repo.

---

## 3. Current Request Pipeline

### Outer Chain

`buildHandler` in [`cmd/gateway/main.go`](./cmd/gateway/main.go) wires:

```go
return middleware.Chain(
    proxyHandler,
    middleware.Tracing(logger),
    middleware.Router(cfg.Routes),
    middleware.Auth(cfg.Auth),
    middleware.RateLimiter(rateLimitStore, logger, ...),
    middleware.UnsupportedFeatures(),
)
```

Because `Chain` applies middleware in reverse order, the live request flow is:

```text
Request -> [Tracing] -> [Router] -> [Auth] -> [RateLimiter] -> [UnsupportedFeatures] -> [Proxy] -> Response
```

#### `Tracing`

- Strips incoming `X-Request-ID`, `X-User-ID`, and `X-User-Role`
- Always generates a fresh UUID request ID
- Writes the generated `X-Request-ID` to both the upstream request and client response
- Logs request start and completion with status and latency

This is a deliberate sanitization boundary. Client-supplied request IDs are not trusted on `main`.

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

- Fails closed for route features that are planned but not implemented
- Returns `501 Not Implemented` when a matched route uses retry config with `max_attempts > 1`

This guard exists so future-facing config does not silently do nothing.

#### `Proxy`

- Serves `gateway-internal` routes directly
- Applies per-route timeout with fallback to server `WriteTimeout`
- Delegates all upstream selection to the transport layer
- Uses `httputil.ReverseProxy` `Rewrite`, not `Director`

---

## 4. Current Transport Pipeline

`transport.NewResilientTransport(nil)` currently returns:

```text
[LoadBalancerTransport] -> [Base http.Transport]
```

There is no retry or circuit-breaker layer on `main` yet.

### Load Balancer Transport

- Reads the matched `RouteConfig` from context
- Selects a target inside the transport layer, not in the proxy director path
- Clones the request before mutating upstream URL/host
- Sets `X-Served-By` from the actual selected upstream host
- Releases least-connection counters when the response body is fully read or closed

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

### Default shipped top-level config fields

The checked-in config also actively uses:

- `server`
- `routes`
- `auth`
- `redis`

The checked-in [`configs/gateway.yaml`](./configs/gateway.yaml) expects `JWT_SECRET` from the
environment and validates `jwt_algorithm: HS256` when any route requires auth.

### Future fields already parsed

These fields exist in config structs today but are not live features yet:

- `retry`
- `circuit_breaker`
- `metrics`
- `logging`

Rules on `main`:

- Keeping these fields in the struct is allowed
- Using unsupported route-level behavior in a live config returns `501`
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

---

## 8. Verification

Current repo verification commands:

```bash
make lint
IRONGATE_TEST_REDIS_ADDR=127.0.0.1:6379 make test
IRONGATE_TEST_REDIS_ADDR=127.0.0.1:6379 make coverage
IRONGATE_TEST_REDIS_ADDR=127.0.0.1:6379 make test-race
make build
```

`make test`, `make coverage`, and `make test-race` require a running Redis instance when you want the Redis-backed integration tests to execute locally. Without `IRONGATE_TEST_REDIS_ADDR`, those Redis integration tests are skipped.

`make coverage` enforces a repo-wide statement coverage floor of 70%.

Key test coverage lives in:

- [`cmd/gateway/main_test.go`](./cmd/gateway/main_test.go)
- [`internal/config/config_test.go`](./internal/config/config_test.go)
- [`internal/middleware/auth_test.go`](./internal/middleware/auth_test.go)
- [`internal/middleware/ratelimit_test.go`](./internal/middleware/ratelimit_test.go)
- [`internal/ratelimit/store_test.go`](./internal/ratelimit/store_test.go)
- [`internal/transport/loadbalancer/loadbalancer_test.go`](./internal/transport/loadbalancer/loadbalancer_test.go)
- [`services/user-service/main_test.go`](./services/user-service/main_test.go)

---

## 9. Planned Extensions

The target end-state still uses the same architectural split:

- outer `http.Handler` middleware for request-level concerns
- inner `http.RoundTripper` layers for transport-level concerns

Planned order when later phases land:

```text
Outer: [Tracing] -> [Router] -> [Auth] -> [RateLimiter] -> [Proxy]
Inner: [Retry] -> [LoadBalancer] -> [CircuitBreaker] -> [BaseTransport]
```

Treat that as the roadmap, not the current runtime.
