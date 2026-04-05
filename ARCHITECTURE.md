# IronGate — Implementation Reference

> This is the implementation reference for the current `main` branch.
>
> Project status: in progress. `main` has shipped Phase 1 foundation and Phase 2 load balancing. Later phases remain planned, not implemented yet.
>
> For target end-state scope and design, see [`PROJECT_SPEC.md`](./PROJECT_SPEC.md) and [`DESIGN_DOC.md`](./DESIGN_DOC.md). If either conflicts with this file, this file wins for the current runtime.

---

## 1. Current Main Snapshot

### Shipped on `main`

- Reverse proxy gateway built with `net/http` and `httputil.ReverseProxy`
- Outer middleware chain: `Tracing -> Router -> UnsupportedFeatures -> Proxy`
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
- Docker Compose with:
  - `gateway`
  - `user-service-1`, `user-service-2`
  - `order-service-1`, `order-service-2`
  - `payment-service-1`

### Planned, not shipped yet

- JWT auth middleware
- Redis-backed rate limiting
- Retry transport
- Circuit breaker transport
- Metrics, Prometheus, and Grafana
- Config hot reload
- Graceful shutdown and readiness endpoints

The codebase already contains some future-facing config fields so later phases can plug into the same route model. On `main`, unsupported route features fail closed instead of being silently ignored.

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
│   │   ├── router.go
│   │   ├── tracing.go
│   │   └── unsupported.go
│   ├── proxy/
│   │   └── proxy.go
│   ├── response/
│   │   └── response.go
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
    middleware.UnsupportedFeatures(),
)
```

Because `Chain` applies middleware in reverse order, the live request flow is:

```text
Request -> [Tracing] -> [Router] -> [UnsupportedFeatures] -> [Proxy] -> Response
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

#### `UnsupportedFeatures`

- Fails closed for route features that are planned but not implemented
- Returns `501 Not Implemented` when a matched route uses:
  - `auth_required: true`
  - non-nil `rate_limit`
  - retry config with `max_attempts > 1`

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
- `timeout`
- `targets`
- `load_balancer`

### Future fields already parsed

These fields exist in config structs today but are not live features yet:

- `auth_required`
- `rate_limit`
- `retry`
- `circuit_breaker`
- `auth`
- `redis`
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
- `X-Served-By` on the response back to the client

They do **not** yet see authenticated identity headers populated by gateway auth, because auth is not implemented on `main`.

---

## 8. Verification

Current repo verification commands:

```bash
make lint
make test
make test-race
make build
```

Key test coverage lives in:

- [`cmd/gateway/main_test.go`](./cmd/gateway/main_test.go)
- [`internal/config/config_test.go`](./internal/config/config_test.go)
- [`internal/transport/loadbalancer/loadbalancer_test.go`](./internal/transport/loadbalancer/loadbalancer_test.go)

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
