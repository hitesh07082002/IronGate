# IronGate Progress Tracker

> Status: Phases 1 through 8 and Phase 9 Milestone 1 are complete. Remaining Phase 9 milestones are planned.
>
> Use this file as the delivery checklist. Use [`README.md`](./README.md) for the public overview and [`ARCHITECTURE.md`](./ARCHITECTURE.md) for the current runtime reference.
> Remaining Phase 9 planning docs live in [`docs/phase9-planning/`](./docs/phase9-planning/).

## Phase 1: Foundation
- [x] Initialize Git repo, Go modules
- [x] Project structure: `cmd/gateway/`, `internal/config/`, `internal/middleware/`, `internal/transport/`, `internal/proxy/`
- [x] Config struct with YAML parsing and `Validate()` method
- [x] Basic HTTP server on `:8080`
- [x] Path-matching router (prefix match, strip prefix)
- [x] Router stores matched route config in `context.Context`
- [x] Reverse proxy with `httputil.ReverseProxy`
- [x] Tuned `http.Transport`: `MaxIdleConnsPerHost=100`, `MaxConnsPerHost=100`
- [x] 3 dummy Go services (User :8081, Order :8082, Payment :8083) with mock data
- [x] Chaos endpoints on each service (`/chaos/latency`, `/chaos/errors`, `/chaos/down`, `/chaos/reset`)
- [x] Docker Compose: gateway + 3 services
- [x] Integration tests: request routes to correct service
- [x] Test: overlapping routes (/api/users vs /api/users/login) resolve to the more specific match

## Phase 2: Load Balancing
- [x] Load balancer interface in `internal/transport/loadbalancer/`
- [x] Round-robin implementation
- [x] Weighted round-robin implementation
- [x] Least-connections implementation (in-memory atomic counter)
- [x] Second instances of user-service and order-service in Docker Compose
- [x] `X-Served-By` response header
- [x] Tests: even distribution, weighted distribution, concurrent safety

## Phase 3: Authentication
- [x] JWT parsing and signature verification (`golang-jwt/jwt/v5`)
- [x] Claims extraction (sub, role, exp, iat)
- [x] Auth middleware reads `auth_required` from `context.Context`
- [x] Routes with `auth_required: false` skip auth (no global `public_paths`)
- [x] `/login` endpoint on user-service returns signed HS256 test JWTs (`sub`, `role`, `iat`, `exp`)
- [x] Inject `X-User-ID`, `X-User-Role` headers
- [x] Proper 401 responses with clear error messages
- [x] Tests: valid token, expired, malformed, missing, wrong algorithm
- [x] Test: spoofed `X-User-ID`, `X-User-Role`, and `X-Request-ID` headers are stripped before proxying
- [x] Test: protected routes forward JWT-derived identity and strip the original bearer token before proxying
- [x] Test: login, register, and health stay public; login -> protected route succeeds

## Phase 4: Rate Limiting
- [x] Redis in Docker Compose
- [x] Sliding window algorithm with Redis Lua script (sorted sets)
- [x] Rate limit middleware reads config from `context.Context`
- [x] Client key: authenticated user ID, fallback to IP
- [x] `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset` headers
- [x] `429 Too Many Requests` with `Retry-After` header
- [x] Fail-open when Redis is unreachable (log warning)
- [x] Tests: under limit passes, over limit returns 429, window reset, Redis down = fail-open
- [x] Test: 100 concurrent goroutines hitting rate limit boundary simultaneously (Lua atomicity verification)
- [x] Test: X-Forwarded-For only trusted from known proxy IPs; spoofed header from untrusted source ignored
- [x] Verification: `IRONGATE_TEST_REDIS_ADDR=127.0.0.1:6379 make coverage` enforces a 70% statement coverage floor in local runs and CI

## Phase 5: Circuit Breaker + Retry
- [x] Circuit breaker state machine in `internal/transport/circuitbreaker/`
- [x] Per-target (host:port) circuit breaker registry
- [x] Only count 5xx + upstream transport failures toward threshold (connection failures and upstream timeouts, not 4xx or caller-side deadlines)
- [x] CLOSED -> OPEN -> HALF-OPEN -> CLOSED transitions
- [x] 503 with fallback JSON when circuit is OPEN
- [x] Retry transport in `internal/transport/retry.go`
- [x] Exponential backoff + full jitter: `delay = random(0, base_delay * 2^attempt)`
- [x] Clone request on each retry (RoundTrip must not mutate original)
- [x] Only retry idempotent methods (GET/HEAD/PUT/DELETE/OPTIONS)
- [x] Only retry on 502/503/504 and transient transport failures (including connection failures, `context.DeadlineExceeded`, and network timeouts)
- [x] Skip retries when circuit is OPEN
- [x] Prefer different target on each retry attempt
- [x] `X-Retry-Count`, `X-Retry-Target` response headers
- [x] Per-route retry config from `context.Context`
- [x] Test: PUT with body, first attempt fails, verify body is intact on retry attempt 2
- [x] Test: client disconnects (context cancelled) mid-retry backoff sleep → gateway stops retrying

## Phase 5.5: Integration Checkpoint
- [x] Wire `resilient.go`: retry -> load balancer -> circuit breaker -> base transport
- [x] Integration test: CB open on target A -> LB picks target B
- [x] Integration test: all targets open -> 503 "no healthy targets"
- [x] Integration test: retry picks different target each attempt
- [x] `go test -race` with 100 concurrent goroutines on circuit breaker
- [x] Full end-to-end pipeline test: request through all 8 layers (tracing → router → auth → rate limit → proxy → retry → LB → CB)

## Phase 6: Observability
- [x] Structured JSON logging for every request
- [x] X-Request-ID generation and propagation (tracing middleware)
- [x] Lock the metrics contract to service-only labels
- [x] Prometheus Go client integration
- [x] `/metrics` endpoint with service-level gateway metrics
- [x] Prometheus in Docker Compose with scrape config
- [x] Grafana in Docker Compose with Prometheus data source
- [x] Grafana dashboard: RPS, latency percentiles, error rates, circuit activity, rate limit rejections
- [x] Export dashboard JSON for version control

## Phase 7: Polish & Production Readiness
- [x] Atomic runtime snapshot manager for hot-reloadable request handling
- [x] fsnotify file watcher: parse -> validate -> build new runtime -> swap atomically (keep old snapshot on invalid reload)
- [x] Graceful shutdown (drain in-flight requests)
- [x] `/health` endpoint for gateway
- [x] `/ready` endpoint for gateway
- [x] Comprehensive integration coverage for reload, invalid reload fallback, readiness, and graceful shutdown
- [x] Makefile: `make load-test` (`make build`, `make test`, `make run`, and `make docker-up` were already shipped earlier)
- [x] `demo.sh` automated demo script

## Phase 8: Documentation & Benchmarks
- [x] ADRs for all architectural decisions (see `ADR/`)
- [x] k6 benchmarks: baseline, with rate limiting, full pipeline, during CB transitions
- [x] Benchmark results with graphs in `benchmarks/results/`
- [x] Polished README with architecture diagram, quick start, feature overview
- [x] Demo capture workflow for the 2-minute GIF/video via `scripts/capture-demo.sh` + `artifacts/demo/README.md`
- [x] `ARCHITECTURE.md` finalized with real code references

## Phase 9: Chaos Observatory
- [x] Planning spec reconciled with current repo paths and current runtime docs
- [x] Implementation plan reconciled with current repo paths and current runtime docs
- [x] Locked implementation decisions captured in `docs/phase9-planning/DECISIONS_LOCK.md`
- [x] Milestone 1 shipped in the repo: OpenTelemetry bootstrap, exemplars, `gateway_circuit_state`, admin reset endpoint, and observatory overlay
- [ ] Remaining Chaos Observatory milestones shipped
- [ ] Full Phase 9 demo platform verified on production

## Stretch Goals
- [ ] Phase 9 Chaos Observatory demo platform (tracked in `docs/phase9-planning/`)
- [ ] Token bucket rate limiting (alternative strategy)
- [ ] Canary routing (N% traffic to v2)
- [ ] Health check endpoints for upstream services
- [ ] IP allowlist/blocklist middleware
- [ ] Adaptive concurrency limiting
