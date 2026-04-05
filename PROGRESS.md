# IronGate Build Progress

## Phase 1: Foundation (Week 1-2)
- [ ] Initialize Git repo, Go modules
- [ ] Project structure: `cmd/gateway/`, `internal/config/`, `internal/middleware/`, `internal/transport/`, `internal/proxy/`
- [ ] Config struct with YAML parsing and `Validate()` method
- [ ] Basic HTTP server on `:8080`
- [ ] Path-matching router (prefix match, strip prefix)
- [ ] Router stores matched route config in `context.Context`
- [ ] Reverse proxy with `httputil.ReverseProxy`
- [ ] Tuned `http.Transport`: `MaxIdleConnsPerHost=100`, `MaxConnsPerHost=100`
- [ ] 3 dummy Go services (User :8081, Order :8082, Payment :8083) with mock data
- [ ] Chaos endpoints on each service (`/chaos/latency`, `/chaos/errors`, `/chaos/down`, `/chaos/reset`)
- [ ] Docker Compose: gateway + 3 services
- [ ] Integration tests: request routes to correct service
- [ ] Test: overlapping routes (/api/users vs /api/users/login) resolve to the more specific match

## Phase 2: Load Balancing (Week 2-3)
- [x] Load balancer interface in `internal/transport/loadbalancer/`
- [x] Round-robin implementation
- [x] Weighted round-robin implementation
- [x] Least-connections implementation (in-memory atomic counter)
- [x] Second instances of user-service and order-service in Docker Compose
- [x] `X-Served-By` response header
- [x] Tests: even distribution, weighted distribution, concurrent safety

## Phase 3: Authentication (Week 3)
- [ ] JWT parsing and signature verification (`golang-jwt/jwt/v5`)
- [ ] Claims extraction (sub, role, exp, iat)
- [ ] Auth middleware reads `auth_required` from `context.Context`
- [ ] Routes with `auth_required: false` skip auth (no global `public_paths`)
- [ ] `/login` endpoint on user-service returns test JWTs
- [ ] Inject `X-User-ID`, `X-User-Role` headers
- [ ] Proper 401 responses with clear error messages
- [ ] Tests: valid token, expired, malformed, missing, wrong algorithm
- [ ] Test: X-User-ID, X-User-Role, X-Request-ID headers stripped from incoming client requests before proxying

## Phase 4: Rate Limiting (Week 3-4)
- [ ] Redis in Docker Compose
- [ ] Sliding window algorithm with Redis Lua script (sorted sets)
- [ ] Rate limit middleware reads config from `context.Context`
- [ ] Client key: authenticated user ID, fallback to IP
- [ ] `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset` headers
- [ ] `429 Too Many Requests` with `Retry-After` header
- [ ] Fail-open when Redis is unreachable (log warning)
- [ ] Tests: under limit passes, over limit returns 429, window reset, Redis down = fail-open
- [ ] Test: 50 concurrent goroutines hitting rate limit boundary simultaneously (Lua atomicity verification)
- [ ] Test: X-Forwarded-For only trusted from known proxy IPs; spoofed header from untrusted source ignored

## Phase 5: Circuit Breaker + Retry (Week 4-5)
- [ ] Circuit breaker state machine in `internal/transport/circuitbreaker/`
- [ ] Per-target (host:port) circuit breaker registry
- [ ] Only count 5xx + connection failures toward threshold (not 4xx)
- [ ] CLOSED -> OPEN -> HALF-OPEN -> CLOSED transitions
- [ ] 503 with fallback JSON when circuit is OPEN
- [ ] Retry transport in `internal/transport/retry.go`
- [ ] Exponential backoff + full jitter: `delay = random(0, base_delay * 2^attempt)`
- [ ] Clone request on each retry (RoundTrip must not mutate original)
- [ ] Only retry idempotent methods (GET/HEAD/PUT/DELETE)
- [ ] Only retry on 502/503/504 and connection failures
- [ ] Skip retries when circuit is OPEN
- [ ] Prefer different target on each retry attempt
- [ ] `X-Retry-Count`, `X-Retry-Target` response headers
- [ ] Per-route retry config from `context.Context`
- [ ] Test: PUT with body, first attempt fails, verify body is intact on retry attempt 2
- [ ] Test: client disconnects (context cancelled) mid-retry backoff sleep → gateway stops retrying

## Phase 5.5: Integration Checkpoint (Mid Week 5)
- [ ] Wire `resilient.go`: retry -> load balancer -> circuit breaker -> base transport
- [ ] Integration test: CB open on target A -> LB picks target B
- [ ] Integration test: all targets open -> 503 "no healthy targets"
- [ ] Integration test: retry picks different target each attempt
- [ ] `go test -race` with 100 concurrent goroutines on circuit breaker
- [ ] Full end-to-end pipeline test: request through all 8 layers (tracing → router → auth → rate limit → proxy → retry → LB → CB)

## Phase 6: Observability (Week 5-6)
- [ ] Structured JSON logging for every request
- [ ] X-Request-ID generation and propagation (tracing middleware)
- [ ] Prometheus Go client integration
- [ ] `/metrics` endpoint with all gateway metrics
- [ ] Prometheus in Docker Compose with scrape config
- [ ] Grafana in Docker Compose with Prometheus data source
- [ ] Grafana dashboard: RPS, latency percentiles, error rates, circuit states, rate limit rejections
- [ ] Export dashboard JSON for version control

## Phase 7: Polish & Production Readiness (Week 6-7)
- [ ] Refactor Config to `atomic.Pointer[Config]` for hot-reload
- [ ] fsnotify file watcher: parse -> validate -> store (keep old on invalid)
- [ ] Graceful shutdown (drain in-flight requests)
- [ ] `/health` and `/ready` endpoints for gateway
- [ ] Comprehensive integration test suite
- [ ] Makefile: `make build`, `make test`, `make run`, `make docker-up`, `make load-test`
- [ ] `demo.sh` automated demo script

## Phase 8: Documentation & Benchmarks (Week 7-8) -- NON-NEGOTIABLE
- [ ] ADRs for all architectural decisions (see `ADR/` directory)
- [ ] k6 benchmarks: baseline, with rate limiting, full pipeline, during CB transitions
- [ ] Benchmark results with graphs in `benchmarks/results/`
- [ ] Polished README with architecture diagram, quick start, feature overview
- [ ] Demo GIF or video (2 minutes)
- [ ] `ARCHITECTURE.md` finalized with real code references

## Stretch Goals
- [ ] Chaos control panel (single HTML page)
- [ ] Token bucket rate limiting (alternative strategy)
- [ ] Canary routing (N% traffic to v2)
- [ ] Health check endpoints for upstream services
- [ ] IP allowlist/blocklist middleware
- [ ] Adaptive concurrency limiting
