# IronGate — Technical Design Document

> **Status:** Approved target design, implementation in progress
> **Author:** Hitesh Sadhwani
> **Last Updated:** April 2026
>
> For project scope and feature requirements, see [`PROJECT_SPEC.md`](./PROJECT_SPEC.md).
> For current implementation reference, see [`ARCHITECTURE.md`](./ARCHITECTURE.md).

---

## 1. Summary

IronGate is being built as a configurable API gateway in Go using a two-tier middleware pipeline — the same pattern production gateways like Traefik use. The target outer chain (`http.Handler`) handles request-level concerns (tracing, routing, auth, rate limiting), while the target inner chain (`http.RoundTripper`) handles transport-level concerns (retry, load balancing, circuit breaking). This separation is what makes retry-aware load balancing and per-target circuit breaking possible.

Current status on `main`: the two-tier split already exists, and tracing, routing, auth, Redis-backed rate limiting, proxy, retry, load balancing, circuit breaking, and Prometheus-backed observability are live.

This document covers the target architecture, algorithms, failure modes, and key tradeoffs. Section 8 links to the ADR set that captures those decisions.

---

## 2. Architecture

### 2.1 Two-Tier Pipeline

IronGate uses two distinct middleware layers with different Go interfaces.

**Current `main` snapshot:**

```text
Outer: [Tracing] -> [Router] -> [Auth] -> [RateLimiter] -> [Proxy]
Inner: [Retry] -> [LoadBalancer] -> [CircuitBreaker] -> [Base HTTP Transport]
```

The sections below now describe the live Phase 5 steady-state ordering. Later phases add observability and operational tooling around the same request and transport split.

**Outer chain — `http.Handler` middleware (request-level):**

Each middleware wraps the next handler. Applied in reverse order so the first-listed is outermost:

```text
Request → [Tracing] → [Router] → [Auth] → [RateLimiter] → [Proxy] → Response
```

- **Tracing** sanitizes any incoming request ID and generates the `X-Request-ID` used for the request lifecycle.
- **Router** matches path to route config, stores config in `context.Context`.
- **Auth** reads `auth_required` from context, validates JWT, injects `X-User-ID`/`X-User-Role`, and strips the bearer token before proxying protected requests.
- **RateLimiter** reads rate limit config from context, checks Redis sliding window.
- **Proxy** is `httputil.ReverseProxy` with a custom `Transport` (the inner chain).

**Inner chain — `http.RoundTripper` (transport-level):**

Lives inside the Proxy's `Transport` field. Each layer implements `RoundTrip(*Request) (*Response, error)`:

```text
Proxy calls Transport.RoundTrip(req):
  → [Retry] → [LoadBalancer] → [CircuitBreaker] → [Base HTTP Transport]
```

- **Retry** clones the request, applies exponential backoff + full jitter, re-invokes the load balancer on each attempt.
- **LoadBalancer** selects a target (host:port) based on strategy (round-robin, weighted, least-conn).
- **CircuitBreaker** checks the per-target circuit state. OPEN → reject. CLOSED/HALF-OPEN → forward.
- **Base HTTP Transport** is `http.Transport` with tuned connection pool.

### 2.2 Why Two Tiers

In a flat middleware chain:
- Retry can't re-invoke the load balancer — it's already past it.
- Circuit breaker can't know the specific target — the load balancer hasn't picked one yet.

The two-tier design solves both problems:
- Retry wraps the entire LB → CB → transport sequence. Each attempt gets a fresh target.
- Circuit breaker sees the specific target that the load balancer selected.
- "All targets down" is a distinct path: LB has no healthy targets → 503.

See [ADR-001: Two-Tier Pipeline](./ADR/001-two-tier-pipeline.md).

### 2.3 Why This Middleware Order

The outer chain ordering is intentional:

1. **Tracing first** — every log line needs the request ID, so it must be generated before anything else runs.
2. **Router second** — resolves which route matched and stores the full `RouteConfig` in `context.Context`. All downstream middleware reads config from context.
3. **Auth before RateLimiter** — per-user rate limits require identity. Without auth running first, there's no user ID to key the rate limiter on. Also prevents unauthenticated requests from burning legitimate users' quota.
4. **Proxy last** — delegates to the inner transport chain after all request-level checks pass.

See [ADR-002: Auth Before Rate Limiting](./ADR/002-auth-before-rate-limiting.md).

### 2.4 System Diagram

```text
                     ┌─────────────────────────────────────────────┐
                     │                DOCKER NETWORK                │
                     │                                              │
 Client ──HTTP──►  ┌─┴───────────────────────────────────────────┐  │
                   │           API GATEWAY (:8080)               │  │
                   │                                             │  │
                   │  ┌── OUTER CHAIN (http.Handler) ──────────┐ │  │
                   │  │  [Tracing] → [Router] → [Auth]         │ │  │
                   │  │  → [RateLimiter] → [Proxy]            │ │  │
                   │  └────────────────────────────────────────┘ │  │
                   │                    │                         │  │
                   │  ┌── INNER CHAIN (http.RoundTripper) ─────┐ │  │
                   │  │  [Retry] → [LoadBalancer]              │ │  │
                   │  │  → [CircuitBreaker] → [HTTP Transport] │ │  │
                   │  └────────────────────────────────────────┘ │  │
                   │                    │                         │  │
                   │  Metrics ──► Prometheus (:9090) ──► Grafana │  │
                   └────────────────────┼────────────────────────┘  │
                                        │                            │
                      ┌─────────────────┼─────────────────┐         │
                      ▼                 ▼                 ▼         │
                 User Service     Order Service     Payment Svc     │
                   :8081            :8082              :8083         │
                      │                 │                 │         │
                      └─────────────────┴─────────────────┘         │
                     └──────────────────────────────────────────────┘
                                        │
                                   Redis :6379
                              (rate limit counters)
```

---

## 3. Request Lifecycle

Full walk-through: `GET /api/orders/42` with a valid Bearer token.

**Outer Chain:**

| Step | Component | Action |
|------|-----------|--------|
| 1 | Client | Sends `GET /api/orders/42` with `Authorization: Bearer <jwt>` |
| 2 | Tracing | Generates `X-Request-ID: "req-a1b2c3d4"`, starts timer |
| 3 | Router | Matches `/api/orders/*` → order-service config. Strips prefix: `/api/orders/42` → `/orders/42`. Stores `RouteConfig` in `context.Context`. |
| 4 | Auth | Reads `auth_required: true` from context. Validates JWT. Extracts `{user_id: "u-789", role: "admin"}`. Adds `X-User-ID`, `X-User-Role` headers and removes the original bearer token before proxying. |
| 5 | RateLimiter | Reads config from context: sliding window, 50 req/60s. Key: `"u-789"`. Redis Lua: 45 < 50 → allow. Sets `X-RateLimit-Remaining: 5`. |
| 6 | Proxy | Calls `Transport.RoundTrip(req)` → enters inner chain. |

**Inner Chain:**

| Step | Component | Action |
|------|-----------|--------|
| 7 | Retry | Reads retry config from context: `max_attempts=3`. GET is idempotent → retries allowed. Clones the request. |
| 8 | LoadBalancer | order-service has 2 targets. Round-robin counter 17 → picks `order-service-2:8092`. Sets request URL. |
| 9 | CircuitBreaker | Checks state for `order-service-2:8092`: CLOSED. Allows request through. |
| 10 | HTTP Transport | Sends `GET http://order-service-2:8092/orders/42`. Reuses idle connection from pool. Gets `200 OK`. |

**Response Path:**

| Step | Component | Action |
|------|-----------|--------|
| 11 | CircuitBreaker | Records success for `order-service-2:8092`. |
| 12 | Retry | Success on first attempt. No retries needed. |
| 13 | Proxy | Streams response back through outer chain. |
| 14 | Metrics | Records service-level series such as `gateway_requests_total{service="order-service"}`, `gateway_request_duration_seconds`, `gateway_upstream_duration_seconds`, and `gateway_open_circuits`. |
| 15 | Client | Receives `200 OK` with `X-Request-ID`, `X-RateLimit-Remaining: 5`, `X-Retry-Count: 0` |

The shipped observability contract is intentionally narrow: every application metric uses only the `{service}` label. Target, path, method, status, user, and client labels stay out of the exported series so the gateway can expose stable cardinality as traffic scales.

---

## 4. Technical Design

### 4.1 Routing & Context Propagation

Router resolves the matched route and stores the full `RouteConfig` in the request context using a typed key:

```go
type contextKey string
const RouteConfigKey contextKey = "routeConfig"

ctx := context.WithValue(req.Context(), RouteConfigKey, route)
req = req.WithContext(ctx)
```

Every downstream middleware reads config from context, never from a global lookup. This is:
- **Thread-safe** — context is immutable and passed per-request.
- **Testable** — unit tests create a context with test config, no global state to reset.
- **Hot-reload ready** — when config changes, new requests get the new config via context. In-flight requests keep their original config.

See [ADR-007: Context for Route Config](./ADR/007-context-for-route-config.md).

### 4.2 Rate Limiting — Sliding Window Algorithm

**Algorithm:**

Count requests in the last N seconds. If count ≥ limit, reject with 429.

**Implementation using Redis Sorted Sets + Lua script:**

```text
Key: rate_limit:{client_key}:{route.Path}

1. ZREMRANGEBYSCORE key  0  (now - window)    — remove expired entries
2. ZCARD key                                   — count entries in window
3. if count < limit → ZADD key now requestId   — unique member per request (`X-Request-ID`)
4. if count >= limit → reject (429)
5. EXPIRE key window                           — set TTL for cleanup

Note: member MUST be unique (e.g., X-Request-ID). Using the timestamp as
member causes undercounting when two requests arrive at the same millisecond.
```

**Why a Lua script (atomicity):**

Without atomicity, a race condition exists: two requests check `count=99` simultaneously, both see `< 100`, both add themselves → `count=101`. The Lua script executes all steps as a single atomic operation on the Redis server. No `WATCH/MULTI` (optimistic locking) needed, which would cause constant retries under load.

**Why sliding window over token bucket (for core):**

Sliding window means "100 requests per 60 seconds" — exactly 100 in any 60-second window. Token bucket means "refill 1.67 tokens/sec, burst up to 100" — allows short bursts. Sliding window is simpler, more intuitive, and used by Cloudflare and GitHub. Token bucket is a stretch goal.

See [ADR-005: Sliding Window Over Token Bucket](./ADR/005-sliding-window-over-token-bucket.md).

### 4.3 Circuit Breaker — State Machine

```text
         failures >= threshold
CLOSED ──────────────────────────→ OPEN
  ↑                                  │
  │ successes >= threshold           │ timeout expires
  │                                  ↓
  └──────────────────────────── HALF-OPEN
                                     │
                              any failure
                                     ↓
                                   OPEN
```

**States:**

| State | Behavior | Transitions |
|-------|----------|-------------|
| CLOSED | Normal operation. Count failures in sliding window. | → OPEN when failures ≥ threshold |
| OPEN | All requests rejected with 503. Timer running. | → HALF-OPEN when timeout expires |
| HALF-OPEN | Allow limited probe requests through. | → CLOSED on successes ≥ threshold. → OPEN on any failure. |

**Per-target scoping:**

Each target (`host:port`) has its own circuit breaker instance, managed by a `Registry`. If `order-service-1:8082` is failing, its circuit opens but `order-service-2:8092` stays closed. The load balancer routes around the broken instance.

**HALF-OPEN probe count:**

In HALF-OPEN state, the circuit allows up to `half_open_max_requests` (default: 3) probe requests through. If all probes succeed (reaching `success_threshold`), the circuit closes. If any probe fails, the circuit reopens immediately. The probe count is configurable because: too few (1) means a single unlucky timeout reopens the circuit on a recovering service; too many floods the recovering service.

**Failure classification:**

Only these count toward the failure threshold:
- 5xx responses (500, 502, 503, 504)
- Connection refused
- Upstream timeout / transport timeout

These do NOT count:
- 4xx responses (client errors, not service failures)
- Caller-originated request deadlines
- Successful responses (obviously)

**Concurrent safety:**

The state machine is accessed by many goroutines simultaneously. State transitions must be race-free, verified by `go test -race` with 100 concurrent goroutines.

### 4.4 Load Balancing Internals

**Round-robin:** Atomic counter, modulo number of healthy targets. `sync/atomic` for lock-free increment.

**Weighted round-robin:** Smooth weighted round-robin (SWRR) — the same algorithm Nginx uses. Each target has a weight; the algorithm distributes requests proportionally without clustering.

**Least connections:** In-memory atomic counter per target. Increment on request start, decrement on response. No distributed state required — this is a single-instance gateway.

See [ADR-006: In-Memory Least Connections](./ADR/006-in-memory-least-connections.md).

### 4.5 Per-Route Timeout

Each route can specify a `timeout` (e.g., `timeout: 30s`). The Proxy middleware wraps the request context with `context.WithTimeout` before calling `Transport.RoundTrip`. If the upstream doesn't respond within the timeout, the context cancels, aborting the in-flight request and any pending retries (no point retrying if the client's deadline has passed). When a route omits `timeout`, the gateway falls back to the server `write_timeout`, which is 30s in the default config.

```go
// In proxy.go, before calling ReverseProxy.ServeHTTP:
routeTimeout := routeCfg.Timeout
if routeTimeout <= 0 {
    routeTimeout = cfg.Server.WriteTimeout
}
ctx, cancel := context.WithTimeout(req.Context(), routeTimeout)
defer cancel()
req = req.WithContext(ctx)
```

If `timeout` is not set on a route, the proxy still applies an upstream deadline by reusing the server `write_timeout`.

### 4.6 Retry & Exponential Backoff

**Backoff formula:**

```text
delay = random(0, min(base_delay × 2^attempt, max_delay))
```

"Full jitter" — delay is random between 0 and the exponential ceiling. This prevents the thundering herd: if 1000 requests fail at t=0, their retries spread across the full delay range instead of all hitting at the same instant.

**Retry + Load Balancer interaction:**

Each retry attempt calls the load balancer again. The LB picks a different target (ideally one that isn't the one that just failed). This is why retry wraps the load balancer in the inner chain — it can re-invoke it.

**Retry + Circuit Breaker interaction:**

- If the circuit is OPEN, skip retries. The target is already known to be down. Fail fast.
- If a retry succeeds (on a different target), the circuit breaker for the failed target still records the failure. The retry is transparent to the circuit breaker.
- If all retry attempts fail and all circuits open, the gateway returns 503 "no healthy targets."

**Request cloning:**

Go's `RoundTrip` contract requires that `RoundTrip` must not mutate the original request. Each retry attempt clones with `req.Clone(req.Context())`.

**Body buffering for retryable requests:**

`req.Clone()` does NOT clone the body. If a request with a body is retried, the second attempt gets an empty body because the first attempt already consumed the `io.ReadCloser`. For retryable requests with bodies: buffer the body into a `[]byte` before the retry loop, then re-attach via `io.NopCloser(bytes.NewReader(buf))` on each clone. This means retried requests consume O(body_size) memory. Since POST is excluded from retries by default, this only applies when retry is explicitly enabled for non-idempotent methods.

### 4.7 JWT Validation Flow

```text
1. Extract token from Authorization: Bearer <token>
2. Decode header → verify algorithm matches config (prevent alg-switching attack)
3. Verify signature using configured secret
4. Validate claims:
   - exp: reject if expired
   - iat: reject if in the future
5. Extract user identity: sub → X-User-ID, role → X-User-Role
6. Add identity headers and remove the original `Authorization` header before proxying the protected request
```

Auth reads `auth_required` from the route config in `context.Context`. Routes with `auth_required: false` skip validation entirely. There is no global `public_paths` list — the auth decision is per-route.

See [ADR-004: Per-Route Auth](./ADR/004-per-route-auth-not-global-public-paths.md).

### 4.8 Structured Logging

Use Go's stdlib `log/slog` (Go 1.21+) with JSON output. Every log entry includes these standard fields:

| Field | Type | Source |
|-------|------|--------|
| `ts` | string (ISO 8601) | slog default |
| `level` | string (INFO/WARN/ERROR) | slog default |
| `msg` | string | per-log-site |
| `request_id` | string | from `X-Request-ID` in context |
| `service` | string | from matched route config |
| `method` | string | `req.Method` |
| `path` | string | `req.URL.Path` |
| `status` | int | response status code |
| `duration_ms` | float64 | measured per-request |
| `error` | string (optional) | only on errors |

All middleware adds `request_id` and `service` to the logger via `slog.With()` so every log line from that request carries correlation context. No ad-hoc field names. `err` vs `error` or `dur` vs `duration_ms` inconsistencies are bugs.

---

## 5. Failure Modes

Each middleware has a defined behavior when its backing infrastructure fails:

| Middleware | Failure Scenario | Strategy | Response |
|-----------|-----------------|----------|----------|
| Rate Limiter | Redis unreachable | **Fail-open** — allow request | 200 (log warning) |
| Auth | JWT secret misconfigured | **Fail-closed** — reject all | 500 Internal Server Error |
| Circuit Breaker | State tracking error | **Fail-open** — treat as CLOSED | Forward request normally |
| Retry | Backoff calculation error | **Fail-open** — skip retries | Return original error |
| Config | Malformed YAML at startup | **Fail-closed** — refuse to start | Exit with error |
| Config | Malformed YAML during hot-reload | **Fail-safe** — keep old config | Log error, continue |
| Proxy | All upstreams unreachable | Return error | 502 Bad Gateway |
| Load Balancer | All circuits open | Return error | 503 "no healthy targets" |

**Why fail-open for rate limiting:** Rejecting legitimate traffic due to a Redis blip is worse than briefly allowing extra traffic through. This is a deliberate tradeoff — availability over strict enforcement during infrastructure degradation.

See [ADR-003: Fail-Open Rate Limiting](./ADR/003-fail-open-rate-limiting.md).

---

## 6. Tech Stack

| Component | Technology | Reasoning |
|-----------|-----------|-----------|
| Gateway | Go (`net/http` + `httputil.ReverseProxy`) | Stdlib has built-in reverse proxy. Every production proxy is Go. |
| Rate Limit Store | Redis + Lua scripts | Industry standard for distributed counters. Lua for atomicity. |
| Config | YAML (`gopkg.in/yaml.v3`) | Human-readable, well-supported in Go. |
| Metrics | Prometheus (`client_golang`) | Pull-based metrics. First-class Go client. |
| Dashboards | Grafana | Connects to Prometheus. Dashboard JSON is version-controllable. |
| Auth | `golang-jwt/jwt/v5` | Standard JWT library for Go. |
| Hot Reload | `fsnotify` | Filesystem event-based, no polling. |
| Load Testing | k6 | JavaScript-based, excellent reporting, open source. |
| Containerization | Docker + Docker Compose | `docker-compose up` = local system (current `main` requires `JWT_SECRET`, `GRAFANA_ADMIN_USER`, and `GRAFANA_ADMIN_PASSWORD` in the environment). Reproducible. |
| TLS (prod) | Caddy | Auto Let's Encrypt. Zero-config HTTPS. |

---

## 7. Testing Strategy

| Level | What | Tool | Coverage Target |
|-------|------|------|----------------|
| **Unit** | Each middleware in isolation: CB state transitions (including 100-goroutine concurrent test + `go test -race`), sliding window edge cases, JWT validation, retry backoff timing, jitter distribution | Go `testing` + `testify` | 80%+ on middleware packages |
| **Integration** | Redis + rate limiter end-to-end, full middleware chain with real HTTP, config reload under load | Go `testing` + `httptest` + real Redis (Docker / CI service) | Full inner transport chain |
| **Load** | Sustained throughput, breaking point, latency percentiles under load | k6 (smoke, load, stress scripts) | 1000+ req/sec baseline |
| **Chaos** | Circuit breaker trips on service kill, retry recovers on transient failure, rate limiter fail-open on Redis kill | k6 + chaos endpoints on dummy services | All failure modes exercised |

---

## 8. Architecture Decision Records

The ADRs for this project live in [`./ADR/`](./ADR/). This section is the index for that decision set.

| ADR | Decision | Key Tradeoff |
|-----|----------|-------------|
| [001](./ADR/001-two-tier-pipeline.md) | Two-tier pipeline (Handler + RoundTripper) | More complex wiring vs. correct retry/LB/CB interaction |
| [002](./ADR/002-auth-before-rate-limiting.md) | Auth before rate limiting | Per-user rate limits require identity first |
| [003](./ADR/003-fail-open-rate-limiting.md) | Fail-open rate limiting | Availability over strict enforcement during Redis downtime |
| [004](./ADR/004-per-route-auth-not-global-public-paths.md) | Per-route auth (no global public_paths) | Config locality over centralized auth config |
| [005](./ADR/005-sliding-window-over-token-bucket.md) | Sliding window over token bucket (core) | Simplicity and accuracy over burst tolerance |
| [006](./ADR/006-in-memory-least-connections.md) | In-memory least connections | Sufficient for single-instance gateway |
| [007](./ADR/007-context-for-route-config.md) | `context.Context` for route config | Thread-safety and testability over simplicity |
| [008](./ADR/008-standard-middleware-interface.md) | Standard middleware interface | Compatibility with Go ecosystem over custom abstractions |

---

## 9. Landscape Awareness

IronGate is a learning project, not a production competitor. But I should know where it sits:

| Gateway | Language | How IronGate Relates |
|---------|----------|---------------------|
| **Traefik** | Go | Uses the same two-tier pipeline pattern. IronGate's architecture mirrors Traefik's middleware + transport separation. |
| **KrakenD** | Go | Stateless, no database, Unix philosophy. Closest in spirit to IronGate's config-driven approach. |
| **Caddy** | Go | IronGate uses Caddy for TLS termination, not as a competitor. Different layer. |
| **Kong** | Lua/OpenResty | Full lifecycle API management. Much broader scope than IronGate. |
| **Envoy** | C++ | Read the docs for design patterns, not the code. Data plane concepts apply. |

What separates IronGate: it's a learning project that demonstrates understanding of the patterns these tools use. The ADRs, benchmarks, and implementation depth are what make it a portfolio piece, not a competitor.

---

## 10. Resolved Design Questions

| Question | Resolution | Reasoning |
|----------|-----------|-----------|
| Dummy services: Go or Node.js? | **Go** | Keeps the stack homogeneous. The project is about demonstrating Go proficiency. |
| Config access pattern? | **Plain struct (Phase 1–6) → `atomic.Pointer[Config]` (Phase 7)** | Middleware reads per-request route config from `context.Context`, not from a global. The Phase 7 refactor is minimal because middleware already reads from context. |
| Deployment timing? | **Post-completion** | Don't let deployment block the core build. DigitalOcean VPS setup happens after Phase 8. |
| Rate limit algorithm? | **Sliding window (core), token bucket (stretch)** | Sliding window is simpler and more intuitive. Token bucket adds burst tolerance but isn't needed for the core demo. |
