# IronGate — Project Specification

> **Status:** In Progress
> **Author:** Hitesh Sadhwani
> **Last Updated:** April 2026
>
> For technical architecture and algorithms, see [`DESIGN_DOC.md`](./DESIGN_DOC.md).
> For implementation reference, see [`ARCHITECTURE.md`](./ARCHITECTURE.md).

---

## 1. Overview

**IronGate** is a lightweight, configurable API gateway built in Go. The target end-state handles routing, authentication, rate limiting, load balancing, circuit breaking, retry with exponential backoff, and observability through a single YAML config file.

Current status on `main`: Phase 1 foundation, Phase 2 load balancing, Phase 3 JWT authentication, Phase 4 Redis-backed rate limiting, Phase 5 retry plus circuit breaking, and Phase 6 observability are shipped.

Target end-state: a single `docker-compose up` brings up the gateway, backend services, Redis, Prometheus, and Grafana. Current `main` does that for the shipped auth plus load-balancing plus rate-limiting plus resilience plus observability flow, with `JWT_SECRET` supplied through the environment.

---

## 2. Motivation

### Why I'm Building This

I'm a junior backend engineer with 1 year of experience at e2e Cloud. I work around infrastructure daily but haven't built infrastructure myself. Most junior portfolios are CRUD apps with a React frontend and a REST API — they blur together. I want to build something that:

- Proves I understand **how production systems actually work** under the hood
- Shows I can think in terms of **system design**, not just feature development
- Demonstrates skills that are **directly relevant** to infrastructure/platform/backend roles
- Gives me a project I can **talk about for 30 minutes** in any interview without running out of depth

### Why an API Gateway

Every request in a microservices architecture passes through a gateway. It's the single most critical piece of infrastructure — handling auth, rate limiting, routing, resilience, and observability. Companies like Netflix (Zuul), Kong, Cloudflare, and AWS have entire teams dedicated to this.

Building one from scratch means I have to deeply understand:

- Network programming and HTTP internals
- Distributed systems patterns (circuit breaking, rate limiting across nodes)
- Middleware/plugin architecture design
- Performance optimization under load
- Observability and operational thinking

### Why Go

Every serious proxy in production — Traefik, Caddy, KrakenD, Envoy's control plane — is written in Go. The stdlib has `net/http` and `httputil.ReverseProxy` built in. Go is the industry standard for this class of infrastructure, and choosing it sends the right signal for backend/infrastructure roles.

### Constraints

| Constraint | Detail |
|-----------|--------|
| Timeline | 7–8 weeks while working full-time at e2e Cloud |
| Language | Go (stdlib + minimal dependencies) |
| Budget | ~₹2,000/month for DigitalOcean VPS (snapshot/destroy to reduce) |
| Frontend | None. No web UI, no admin dashboard. Pure backend infrastructure. |
| Deployment | Single VPS with Docker Compose + Caddy for TLS termination |

---

## 3. Scope

### 3.1 In Scope

| Feature | Description | Complexity |
|---------|-------------|------------|
| Reverse Proxy Core | Accept HTTP requests, forward to upstream services, return responses | Medium |
| Config-Driven Routing | YAML file defines route → service mappings, hot-reloadable | Medium |
| Load Balancing | Distribute requests across multiple instances (round-robin, weighted, least-conn) | Medium |
| JWT Authentication | Validate tokens, extract claims, reject invalid/expired tokens | Medium |
| Distributed Rate Limiting | Per-client request throttling with Redis-backed sliding window | High |
| Circuit Breaker | Detect failing services, stop sending traffic, auto-recover | High |
| Retry with Backoff | Exponential backoff + full jitter for transient upstream failures | Medium |
| Distributed Tracing | Correlation IDs, structured logging, request lifecycle tracking | Medium |
| Metrics & Dashboards | Prometheus metrics endpoint + pre-built Grafana dashboards | Medium |
| Dockerized Environment | Full system runs with one `docker-compose up` command | Low–Medium |

### 3.2 Out of Scope

| Feature | Reason |
|---------|--------|
| WebSocket / gRPC proxying | Adds complexity without proportional learning value |
| TLS termination | Handled by Caddy; operational, not architectural |
| Admin API / Web UI | Would shift focus to frontend work |
| Service discovery (Consul/etcd) | A separate project; static config is sufficient |
| OAuth2 / OpenID Connect | JWT validation is enough to demonstrate auth middleware |
| API versioning / transformation | Feature of mature gateways, not core patterns |
| Persistent storage / database | Gateway is stateless; Redis is a cache, not a database |
| Multi-cluster / multi-region | Beyond scope; single-node gateway is the goal |

### 3.3 Stretch Goals (Priority Order)

1. **Chaos control panel** — Single HTML page to inject failures and watch Grafana respond live
2. **Token bucket rate limiting** — Alternative algorithm per route via `strategy: "token_bucket"`
3. **Canary routing** — Send N% traffic to v2 of a service based on weight config
4. **Health check endpoints** — Background health checks, auto-remove unhealthy targets
5. **IP allowlist/blocklist** — Per-route IP filtering middleware
6. **Adaptive concurrency limiting** — Auto-adjust limits based on latency (research-grade)

---

## 4. Feature Requirements

> This section defines the target end-state behavior for the full project. `main` currently ships only a subset of these requirements. For current runtime behavior, see [`ARCHITECTURE.md`](./ARCHITECTURE.md) and [`PROGRESS.md`](./PROGRESS.md).

### 4.1 Config-Driven Routing

- All gateway behavior is defined in a single YAML config file (`gateway.yaml`)
- No code changes needed to add routes, change rate limits, or configure services
- Router matches request paths to route configs using prefix matching
- `strip_prefix` removes the matching prefix before forwarding (e.g., `/api/users/1` → `/users/1`)
- Config validation at startup rejects: empty targets, unknown LB strategies, unknown retry jitter values, non-positive rate limits, missing `redis.address` when rate limiting is configured, negative `redis.db`, missing JWT secret, invalid durations
- Hot-reload in Phase 7: file watcher detects changes → parse → validate → swap atomically (keep old config on validation failure)

### 4.2 Rate Limiting

- Sliding window algorithm backed by Redis (Lua scripts for atomicity)
- Client identified by authenticated user ID (from JWT `X-User-ID`); falls back to client IP for unauthenticated routes
  - **Security Rule:** Extract IP from `X-Forwarded-For` *only* if the TCP `RemoteAddr` belongs to a trusted proxy (e.g., Caddy's internal IP). Otherwise, use `RemoteAddr`. This prevents IP spoofing attacks that bypass rate limits.
- Every response includes rate limit headers:
  - `X-RateLimit-Limit` — max requests in window
  - `X-RateLimit-Remaining` — requests left
  - `X-RateLimit-Reset` — Unix timestamp when window resets
- `429 Too Many Requests` with `Retry-After` header when limit exceeded
- **Fail-open:** If Redis is unreachable, allow the request, log a warning, and omit authoritative rate-limit counters for that request

### 4.3 Circuit Breaker

- Three states: CLOSED (normal) → OPEN (blocking) → HALF-OPEN (probing) → CLOSED
- **Per-target** isolation (host:port), not per-service. A failing instance doesn't block healthy ones.
- Only count **5xx responses and connection errors** toward the failure threshold. 4xx errors are client mistakes, not service failures.
- OPEN state returns `503 Service Unavailable` with fallback JSON
- HALF-OPEN allows a limited number of probe requests; success closes the circuit, any failure reopens it
- "No healthy targets" (all circuits open) returns `503` with `{"error": "no healthy targets for service: <name>"}`
- State machine must be concurrent-safe (`go test -race` with 100 goroutines)

### 4.4 Load Balancing

Three strategies, selectable per route:

| Strategy | Behavior |
|----------|----------|
| `round_robin` | Sequential rotation across targets. Fair for equally capable instances. |
| `weighted` | Targets have weights (e.g., 3:1). High-weight targets get proportionally more traffic. |
| `least_conn` | Route to the target with the fewest in-flight requests. Best for variable-latency services. |

- Lives in the inner transport chain so retry can re-invoke it to pick a different target
- `X-Served-By` response header shows which instance handled the request

### 4.5 Retry with Backoff

- Exponential backoff with full jitter: `delay = random(0, base_delay × 2^attempt)`
- Only retry **idempotent methods** by default: GET, HEAD, PUT, DELETE, OPTIONS
- Only retry on **transient transport failures**: 502, 503, 504, connection failures, upstream deadlines, and network timeouts
- Do **not** retry: 4xx errors, POST requests (unless explicitly configured)
- Each retry clones the request (Go's RoundTrip contract)
- Each retry picks a **different target** via the load balancer
- Skip retries entirely when the circuit is OPEN (fail fast)
- Response headers: `X-Retry-Count`, `X-Retry-Target`
- Per-route config: `max_attempts`, `base_delay`, `max_delay`, `jitter`

### 4.6 JWT Authentication

- Extract token from `Authorization: Bearer <token>` header
- Verify signature using configured secret (HS256)
  - **Security Rule:** Explicitly enforce that the parsed token's `alg` header is exactly `HS256`. Do not rely on default parser behavior to prevent "none" algorithm or asymmetric key downgrade attacks. Use constant-time comparison for the signature.
- Validate claims: `exp` (expiration), `iat` (issued at)
  - *Note:* `jti` (JWT ID) is not validated, meaning tokens can be replayed until expiration. This is an accepted tradeoff for v1.
- Auth decision is **per-route** via `auth_required` flag in route config (no global `public_paths`)
- On success: inject `X-User-ID` and `X-User-Role` headers into upstream request
- On protected routes: remove the original bearer `Authorization` header before proxying so downstream services only trust the projected identity headers
- **Header Sanitization Security Rule:** The gateway MUST explicitly strip `X-User-ID`, `X-User-Role`, and `X-Request-ID` from ALL incoming client requests before processing to prevent privilege escalation via header spoofing.
- Error responses:
  - Missing token → `401 {"error": "missing authorization header"}`
  - Malformed token → `401 {"error": "malformed token"}`
  - Expired token → `401 {"error": "token expired"}`
  - Invalid signature → `401 {"error": "invalid token"}`
- **Fail-closed:** If JWT secret is misconfigured, reject all requests

### 4.7 Observability

**Prometheus Metrics:**

| Category | Metrics |
|----------|---------|
| Requests | `gateway_requests_total{service}`, `gateway_request_failures_total{service}`, `gateway_request_duration_seconds{service}` (histogram), `gateway_in_flight_requests{service}` |
| Rate Limiting | `gateway_rate_limit_rejections_total{service}` |
| Circuit Breaker | `gateway_circuit_opens_total{service}`, `gateway_open_circuits{service}` |
| Retries | `gateway_retries_total{service}`, `gateway_retry_delay_seconds{service}` (histogram) |
| Upstream | `gateway_upstream_duration_seconds{service}` (histogram) |

**Label Contract:** Every gateway-exported application series uses only the `{service}` label. The shipped runtime does not export path, user, method, status, client, or target labels.

**Grafana Dashboard Panels:**

- Requests per second by service
- P50 / P95 / P99 request latency by service
- 5xx error rate percentage by service
- Retry activity by service
- Circuit opens and currently open circuits by service
- Rate-limit rejections over time by service
- Upstream P95 latency by service

### 4.8 Distributed Tracing

- Gateway sanitizes incoming `X-Request-ID` and generates a fresh request ID on `main`
- Request ID appears in every structured JSON log entry
- Headers propagated to upstream: `X-Request-ID`, `X-Forwarded-For`, `X-Forwarded-Host`, `X-Forwarded-Proto`, `X-User-ID`, `X-User-Role`
- On protected routes, the original `Authorization` bearer token is not forwarded after gateway auth succeeds
- Trusted request-ID propagation is a possible future enhancement, but it is not the current behavior
- Not OpenTelemetry — lightweight correlation-ID propagation is sufficient for this scope

### 4.9 Standard Error Response Format

All error responses from the gateway follow a consistent JSON structure:

```json
{
  "error": "human-readable error message",
  "code": 429,
  "request_id": "req-a1b2c3d4"
}
```

Every implemented middleware and proxy path should return errors in this format, and later phases must preserve it. The `request_id` field links every error to the distributed trace, making debugging straightforward.

---

## 5. Config Schema

Target end-state schema for the full gateway. The shipped Phase 5 runtime config on `main` is a smaller subset and intentionally omits later-phase fields until those behaviors land.

```yaml
server:
  port: 8080
  read_timeout: 30s
  write_timeout: 30s

routes:
  - path: "/api/users/login"
    strip_prefix: "/api"
    service: "user-service"
    methods: ["POST"]
    auth_required: false
    timeout: 30s
    rate_limit:
      requests: 100
      window: 60s
      strategy: "sliding_window"
    retry:
      max_attempts: 3
      base_delay: 100ms
      max_delay: 2s
      jitter: "full"
    targets:
      - host: "user-service-1"
        port: 8081
        weight: 3
      - host: "user-service-2"
        port: 8091
        weight: 1
    load_balancer: "weighted"

  - path: "/api/users/register"
    strip_prefix: "/api"
    service: "user-service"
    methods: ["POST"]
    auth_required: false
    timeout: 30s
    rate_limit:
      requests: 100
      window: 60s
      strategy: "sliding_window"
    retry:
      max_attempts: 3
      base_delay: 100ms
      max_delay: 2s
      jitter: "full"
    targets:
      - host: "user-service-1"
        port: 8081
        weight: 3
      - host: "user-service-2"
        port: 8091
        weight: 1
    load_balancer: "weighted"

  - path: "/api/users"
    strip_prefix: "/api"
    service: "user-service"
    methods: ["GET", "POST", "PUT", "DELETE"]
    auth_required: true
    timeout: 30s
    rate_limit:
      requests: 100
      window: 60s
      strategy: "sliding_window"
    retry:
      max_attempts: 3
      base_delay: 100ms
      max_delay: 2s
      jitter: "full"
    targets:
      - host: "user-service-1"
        port: 8081
        weight: 3
      - host: "user-service-2"
        port: 8091
        weight: 1
    load_balancer: "weighted"

  - path: "/api/orders"
    strip_prefix: "/api"
    service: "order-service"
    methods: ["GET", "POST"]
    auth_required: true
    rate_limit:
      requests: 50
      window: 60s
      strategy: "sliding_window"
    retry:
      max_attempts: 3
      base_delay: 100ms
      max_delay: 2s
      jitter: "full"
    targets:
      - host: "order-service-1"
        port: 8082
      - host: "order-service-2"
        port: 8092
    load_balancer: "round_robin"

  - path: "/api/payments"
    strip_prefix: "/api"
    service: "payment-service"
    methods: ["GET", "POST"]
    auth_required: true
    rate_limit:
      requests: 20
      window: 60s
      strategy: "sliding_window"
    retry:
      max_attempts: 1                  # No retries — payments are not idempotent
    targets:
      - host: "payment-service-1"
        port: 8083
    load_balancer: "round_robin"

  - path: "/health"
    service: "gateway-internal"
    auth_required: false
    rate_limit: null

circuit_breaker:
  failure_threshold: 5
  success_threshold: 3
  timeout: 30s
  window_size: 60s
  half_open_max_requests: 3
  scope: "per-target"

auth:
  jwt_secret: "${JWT_SECRET}"
  jwt_algorithm: "HS256"

redis:
  address: "redis:6379"
  password: ""
  db: 0

metrics:
  enabled: true
  path: "/metrics"

logging:
  level: "info"
  format: "json"
```

---

## 6. Dummy Microservices

Three services simulate real backend behavior for testing and demos.

### User Service (`:8081`)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/users` | GET | List users |
| `/users/:id` | GET | Single user |
| `/users` | POST | Create user (mock) |
| `/users/login` | POST | Return a signed test JWT |
| `/users/register` | POST | Create account (public, no auth) |

### Order Service (`:8082`)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/orders` | GET | List orders |
| `/orders/:id` | GET | Single order |
| `/orders` | POST | Create order (mock) |

### Payment Service (`:8083`)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/payments` | POST | Process payment (mock) |
| `/payments/:id` | GET | Payment status |

### Chaos Endpoints (All Services)

| Endpoint | Method | Behavior |
|----------|--------|----------|
| `/chaos/latency` | POST | Add artificial latency (`{"delay_ms": 2000}`) |
| `/chaos/errors` | POST | Return 500 at configurable rate (`{"rate": 0.5}`) |
| `/chaos/down` | POST | Stop responding (circuit breaker testing) |
| `/chaos/reset` | POST | Restore normal behavior |

---

## 7. Development Phases

| Phase | Focus | Duration | Deliverable | Risk |
|-------|-------|----------|-------------|------|
| **1** | Foundation — reverse proxy + config routing | Week 1–2 | `curl localhost:8080/api/users/1` routes correctly | Config `Validate()` + tuned transport from day 1 |
| **2** | Load Balancing | Week 2–3 | Requests alternate across instances | Least-conn atomic counter must be goroutine-safe |
| **3** | JWT Authentication | Week 3 | Invalid JWTs get 401, valid tokens pass with headers | Per-route `auth_required`, not global `public_paths` |
| **4** | Distributed Rate Limiting | Week 3–4 | 429s with proper headers after burst | Redis Lua scripts are a hidden time sink |
| **5** | Circuit Breaker + Retry | Week 4–5 | Kill service → circuit opens → recovery | Most complex phase. CB + retry interaction is critical. |
| **5.5** | Integration Checkpoint | Mid Week 5 | Full inner transport chain verified | `go test -race` with 100 goroutines |
| **6** | Observability | Week 5–6 | Live Grafana dashboard | Build dashboard JSON early with placeholder data |
| **7** | Polish & Production Readiness | Week 6–7 | Graceful shutdown, hot reload, Makefile | **Cuttable** if time is tight |
| **8** | Documentation & Benchmarks | Week 7–8 | ADRs, benchmark graphs, polished README, demo | **Non-negotiable** — cut Phase 7 before Phase 8 |

> **Phase 8 is non-negotiable.** The documentation, benchmarks, and ADRs are what make this a portfolio project instead of a code dump. If time runs short, cut hot-reload and graceful shutdown from Phase 7.

---

## 8. Deployment & Hosting

### Infrastructure

The entire project runs on a **single DigitalOcean VPS** with a custom domain. No managed databases, no cloud-specific services.

| Resource | Spec | Reasoning |
|----------|------|-----------|
| RAM | 4 GB minimum | 7–8 containers (gateway + 3 services + Redis + Prometheus + Grafana) |
| CPU | 2 vCPUs | Sufficient for concurrent requests and load tests |
| Disk | 50 GB SSD | Prometheus retention, Docker images, logs |
| OS | Ubuntu 24.04 LTS | Stable, Docker installs cleanly |
| Cost | ~$24/month | Snapshot and destroy when not in use to reduce |

### Domain & TLS

```
gateway.yourdomain.com    → VPS:8080  (API Gateway)
grafana.yourdomain.com    → VPS:3000  (Grafana Dashboard)
```

TLS via **Caddy** (automatic Let's Encrypt):

```
Internet → Caddy (TLS termination) → API Gateway (:8080) → Services
```

This mirrors production setups where TLS termination happens at the edge, not inside the application.

### Traffic Flow

```
Client (HTTPS :443)
    │
    ▼
Caddy (TLS termination, auto Let's Encrypt)
    │
    ▼ HTTP :8080 (internal)
API Gateway
    │
    ├──→ User Service    :8081
    ├──→ Order Service   :8082
    └──→ Payment Service :8083

Also running:
  Redis      :6379  (internal only)
  Prometheus :9090  (internal, scraped by Grafana)
  Grafana    :3000  (exposed via Caddy)
```

### Security

- **Firewall (UFW):** Only ports 22 (SSH), 80 (HTTP redirect), 443 (HTTPS)
- **Redis / Prometheus:** Internal Docker network only, not exposed to internet
- **Grafana:** Password-protected, HTTPS only
- **SSH:** Key-based auth, password login disabled

---

## 9. Success Criteria

- [ ] `docker-compose up` starts the entire system in under 60 seconds
- [ ] Requests route correctly to all 3 services based on path
- [ ] Load balancing distributes requests across multiple instances
- [ ] Invalid/expired JWTs are rejected with clear error messages
- [ ] Rate limiting returns 429 with proper `X-RateLimit-*` headers
- [ ] Circuit breaker opens on service failure, recovers on restart
- [ ] Retries handle transient failures transparently with backoff + jitter
- [ ] Every request has a correlation ID flowing through the system
- [ ] Grafana dashboard shows real-time metrics
- [ ] Benchmarks show gateway handles 1000+ req/sec at p99 < 100ms (baseline, 2-vCPU)
- [ ] README tells a complete story with diagrams, quick start, and demo
- [ ] Architecture decisions documented with tradeoffs in ADRs
- [ ] Project can be demoed in under 5 minutes with `demo.sh`

## GSTACK REVIEW REPORT

| Review | Trigger | Why | Runs | Status | Findings |
|--------|---------|-----|------|--------|----------|
| Eng Review | `/plan-eng-review` | Architecture & tests (required) | 2 | CLEAR | 0 issues, 0 critical gaps |
| CEO Review | `/plan-ceo-review` | Scope & strategy | 1 | CLEAR | HOLD SCOPE — highly aligned |
| CSO Review | `/cso` | Security boundaries | 1 | CLEAR | 2 critical flaws (patched) |

- **VERDICT:** CEO + CSO + ENG CLEARED — ready to implement.
