# IronGate — Production-Grade API Gateway (Go)

## Project Overview

IronGate is a reverse-proxy API gateway built from scratch using Go's `net/http` standard library. The current implementation ships tracing, routing, load balancing, JWT authentication, Redis-backed sliding-window rate limiting, retry, circuit breaking, Prometheus/Grafana observability, hot reload, readiness, graceful shutdown, a benchmark harness, and recorded benchmark artifacts.

## Key Documentation

- [README.md](./README.md) — Quick start, verification commands, benchmark summary, demo flow
- [PROJECT_SPEC.md](./PROJECT_SPEC.md) — Requirements, scope, feature specs, deployment plan, success criteria
- [DESIGN_DOC.md](./DESIGN_DOC.md) — Algorithms, data flows, failure modes, pseudocode
- [ARCHITECTURE.md](./ARCHITECTURE.md) — Implementation reference: interfaces, rules, directory structure, config schema
- [PROGRESS.md](./PROGRESS.md) — Phase-by-phase build tracker with test requirements
- [ADR/](./ADR/) — Architecture Decision Records (8 total)

## Source Of Truth

- Treat the project root, [`IronGate/`](./), as the only source of truth.
- Treat [`ARCHITECTURE.md`](./ARCHITECTURE.md) as the current-runtime reference when code and docs disagree.
- Treat [`PROJECT_SPEC.md`](./PROJECT_SPEC.md), [`DESIGN_DOC.md`](./DESIGN_DOC.md), and [`PROGRESS.md`](./PROGRESS.md) as the full-project and future-phase references.
- Do not assume planned features are already shipped just because they appear in the full-project docs.

## Architecture (Two-Tier Pipeline)

**Current outer chain** (`http.Handler` middleware): Tracing → Router → Metrics → Auth → RateLimiter → Proxy

**Current inner chain** (`http.RoundTripper` transport): Retry → Load Balancer → Circuit Breaker → Base Transport

Future-phase target architecture lives in [`DESIGN_DOC.md`](./DESIGN_DOC.md) and [`PROJECT_SPEC.md`](./PROJECT_SPEC.md). Later work should preserve this request/transport split unless the runtime contract is intentionally changed.

Router stores the matched `RouteConfig` in `context.Context`. All downstream middleware reads config from context, not globals.

## Engineering Rules

1. Standardized error response: `{"error": string, "code": int, "request_id": string}`
2. Header sanitization: strip `X-User-ID`, `X-User-Role`, `X-Request-ID` from all incoming requests before processing
3. Metrics labels: only `{service}` — never path, user ID, or any unbounded value
4. Never buffer response bodies in middleware
5. Rate limiter client IP: trust `X-Forwarded-For` only from known proxy IPs
6. JWT parsing: explicitly enforce `alg=HS256`, reject `none` and mismatched algorithms
7. Circuit breaker: per-target (`host:port`), only 5xx + upstream transport failures count; caller-side deadlines do not
8. Retry: idempotent methods only (GET, HEAD, PUT, DELETE, OPTIONS). Clone request on each attempt.
9. All state machines must pass `go test -race` with 100 concurrent goroutines
10. Rate limiter fails-open when Redis is unreachable

## Commands

```bash
IRONGATE_TEST_REDIS_ADDR=127.0.0.1:6379 make test       # run all tests, including Redis integration coverage
IRONGATE_TEST_REDIS_ADDR=127.0.0.1:6379 make coverage   # enforce the 70% statement coverage gate
IRONGATE_TEST_REDIS_ADDR=127.0.0.1:6379 make test-race  # run race-enabled tests
make lint       # gofmt check + go vet
make all        # alias to build
make build      # compile gateway binary
make benchmark-test  # exercise the Python benchmark runner contract
make benchmark  # run the full benchmark suite
./demo.sh       # local under-five-minute demo flow
make clean      # remove generated binaries and coverage artifacts
make run        # start gateway on :8080
```

`make test`, `make coverage`, and `make test-race` require a running Redis instance when you want the Redis-backed integration tests to execute locally. Without `IRONGATE_TEST_REDIS_ADDR`, those Redis integration tests are skipped.
Run `mise install` once in the repo root to install the pinned `k6` toolchain used by `make benchmark`, `make load-test`, and `./demo.sh`.

## Config

Gateway runtime config lives in `configs/gateway.yaml`. Current route-level settings include `auth_required`, `timeout`, `rate_limit`, `retry`, `load_balancer`, and `targets`. Top-level `circuit_breaker` and `redis` config are also live.

## Deploy Configuration (configured by /setup-deploy)
- Platform: custom Ubuntu droplet with host-level Caddy and Docker Compose
- Production URL: https://irongate.hiteshsadhwani.xyz
- Deploy workflow: local SSH deploy via `./scripts/deploy-production.sh`
- Deploy status command: `curl -fsS https://irongate.hiteshsadhwani.xyz/ready`
- Merge method: squash
- Project type: API
- Post-deploy health check: `./scripts/check-production-health.sh`

### Custom deploy hooks
- Pre-merge: `make lint && make test && make build`
- Deploy trigger: `./scripts/deploy-production.sh`
- Deploy status: `curl -fsS https://irongate.hiteshsadhwani.xyz/ready`
- Health check: `./scripts/check-production-health.sh`

## Skill Routing

When the user's request matches an available skill, invoke it as the first action:
- Bugs, errors, "why is this broken" → `/investigate`
- Ship, deploy, push, create PR → `/ship`
- QA, test the site → `/qa`
- Code review, check my diff → `/review`
- Architecture review → `/plan-eng-review`
- Security audit → `/cso`
- Save progress, checkpoint → `/checkpoint`
- Code quality, health check → `/health`
