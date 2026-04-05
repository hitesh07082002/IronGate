# IronGate — Production-Grade API Gateway (Go)

## Project Overview

IronGate is a reverse-proxy API gateway built from scratch using Go's `net/http` standard library. The current repo ships the foundation, tracing, routing, load balancing, and JWT authentication. Rate limiting, retry, circuit breaking, and richer observability remain planned.

## Key Documentation

- [PROJECT_SPEC.md](./PROJECT_SPEC.md) — Requirements, scope, feature specs, deployment plan, success criteria
- [DESIGN_DOC.md](./DESIGN_DOC.md) — Algorithms, data flows, failure modes, pseudocode
- [ARCHITECTURE.md](./ARCHITECTURE.md) — Implementation reference: interfaces, rules, directory structure, config schema
- [PROGRESS.md](./PROGRESS.md) — Phase-by-phase build tracker with test requirements
- [INTERVIEW_GUIDE.md](./INTERVIEW_GUIDE.md) — Talking points and "why" rationale for each design decision
- [ADR/](./ADR/) — Architecture Decision Records (8 total)

## Source Of Truth

- Treat the Git repo root, [`IronGate/`](./), as the only project root and source of truth.
- Treat [`ARCHITECTURE.md`](./ARCHITECTURE.md) as the current-runtime reference when code and docs disagree.
- Treat [`PROJECT_SPEC.md`](./PROJECT_SPEC.md), [`DESIGN_DOC.md`](./DESIGN_DOC.md), and [`PROGRESS.md`](./PROGRESS.md) as the full-project and future-phase references.
- Do not assume planned features are already shipped just because they appear in the full-project docs.

## Architecture (Two-Tier Pipeline)

**Current outer chain** (`http.Handler` middleware): Tracing → Router → Auth → UnsupportedFeatures → Proxy

**Current inner chain** (`http.RoundTripper` transport): Load Balancer → Base Transport

Future-phase target architecture lives in [`DESIGN_DOC.md`](./DESIGN_DOC.md) and [`PROJECT_SPEC.md`](./PROJECT_SPEC.md). Later phases add rate limiting to the outer chain and retry plus circuit breaker to the inner chain.

Router stores the matched `RouteConfig` in `context.Context`. All downstream middleware reads config from context, not globals.

## Engineering Rules

1. Standardized error response: `{"error": string, "code": int, "request_id": string}`
2. Header sanitization: strip `X-User-ID`, `X-User-Role`, `X-Request-ID` from all incoming requests before processing
3. Metrics labels: only `{service}` — never path, user ID, or any unbounded value
4. Never buffer response bodies in middleware
5. Rate limiter client IP: trust `X-Forwarded-For` only from known proxy IPs
6. JWT parsing: explicitly enforce `alg=HS256`, reject `none` and mismatched algorithms
7. Circuit breaker: per-target (`host:port`), only 5xx + connection errors count
8. Retry: idempotent methods only (GET, HEAD, PUT, DELETE, OPTIONS). Clone request on each attempt.
9. All state machines must pass `go test -race` with 100 concurrent goroutines
10. Rate limiter fails-open when Redis is unreachable

## Commands

```bash
make test       # run all tests
make test-race  # run race-enabled tests
make lint       # gofmt check + go vet
make build      # compile gateway binary
make run        # start gateway on :8080
```

## Config

Gateway runtime config lives in `configs/gateway.yaml`. Shipped route-level settings on `main` include `auth_required`, `timeout`, `load_balancer`, and `targets`. Future-facing fields like `rate_limit` and `retry` are parsed but fail closed until those phases land.

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
