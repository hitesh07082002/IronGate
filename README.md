# IronGate

IronGate is a configurable API gateway built in Go with the standard `net/http` stack. It combines config-driven routing, JWT authentication, Redis-backed sliding-window rate limiting, retry, load balancing, circuit breaking, Prometheus/Grafana observability, hot reload, and graceful shutdown in one runnable project.

The project is set up to be easy to evaluate: `./demo.sh` exercises the stack end to end, `benchmarks/` contains reproducible k6 scenarios, and the repository includes recorded benchmark artifacts plus architectural decision records.

## Highlights

- Config-driven longest-prefix routing with per-route method allowlists
- JWT auth with explicit `HS256` enforcement and sanitized identity headers
- Redis-backed sliding-window rate limiting with fail-open behavior on Redis outages
- Retry with exponential backoff and full jitter for idempotent methods
- Round-robin, weighted round-robin, and least-connections load balancing
- Per-target circuit breaking with failover and half-open recovery
- Prometheus metrics plus Grafana dashboards with service-only label cardinality
- Hot reload with rollback to the last valid runtime snapshot
- Graceful shutdown that flips `/ready` before draining in-flight requests

## Architecture

```mermaid
flowchart LR
    C["Client"] --> M["Runtime manager"]
    M --> I["/health /ready /metrics"]
    M --> O["Outer chain<br/>Tracing -> Router -> Metrics -> Auth -> RateLimiter -> Proxy"]
    O --> T["Inner transport<br/>Retry -> LoadBalancer -> CircuitBreaker -> Base Transport"]
    O -. limiter .-> R["Redis"]
    T --> U["user-service x2"]
    T --> S["order-service x2"]
    T --> P["payment-service x1"]
    M -. metrics .-> PR["Prometheus"]
    PR --> G["Grafana"]
```

Deeper implementation notes live in [`ARCHITECTURE.md`](./ARCHITECTURE.md), [`DESIGN_DOC.md`](./DESIGN_DOC.md), and [`ADR/`](./ADR/).

## Quick Start

### Fastest path

```bash
./demo.sh
```

`demo.sh` bootstraps the local stack, waits for `/ready`, issues a login token, exercises protected user, order, and payment routes, samples `/metrics`, and finishes with the k6 smoke test.

### Manual stack

```bash
export JWT_SECRET=demo-secret
export GRAFANA_ADMIN_USER=admin
export GRAFANA_ADMIN_PASSWORD=admin

docker-compose up -d --build
curl -fsS http://127.0.0.1:8080/ready
curl -fsS -X POST http://127.0.0.1:8080/api/users/login
```

## Verification

Run the full local verification suite:

```bash
make lint
make build
IRONGATE_TEST_REDIS_ADDR=127.0.0.1:6379 make test
IRONGATE_TEST_REDIS_ADDR=127.0.0.1:6379 make test-race
IRONGATE_TEST_REDIS_ADDR=127.0.0.1:6379 make coverage
make benchmark-test
mise x k6@1.7.1 -- make benchmark
```

`make benchmark` writes a timestamped result bundle under `benchmarks/results/`.
`make benchmark-test` covers the Python benchmark runner's artifact-rendering and dependency-check contract without needing k6 or Docker.

## Demo

`./demo.sh` is the recommended walkthrough. It brings up the stack, waits for readiness, issues a login token, exercises protected routes, samples `/metrics`, and finishes with the smoke benchmark.

For a shareable demo asset, use [`scripts/capture-demo.sh`](./scripts/capture-demo.sh). Generated transcripts always land under [`artifacts/demo/`](./artifacts/demo/README.md), and the built-in MP4 path is wired for macOS `ffmpeg`/`avfoundation`; large binaries are intentionally not committed.

## Benchmark Summary

Recorded benchmark bundle: [`benchmarks/results/20260406-033854-d1edb38/`](./benchmarks/results/20260406-033854-d1edb38/README.md)

Committed run environment:

- Apple M4, 10 logical CPU cores, 16 GB RAM
- `k6 v1.7.1`
- Docker Compose `v2.35.1`
- Go `1.24.4`

Main scenario highlights from that run:

| Scenario | Contract | Result |
|---|---|---:|
| Baseline public routing | `POST /api/users/login`, 24 VUs, 20s, distributed client IPs | `3799.53 req/s`, `p50 4.82 ms`, `p95 12.90 ms`, `p99 31.96 ms` |
| Authenticated + rate-limited traffic | `GET /api/payments/p-1`, 8 VUs, 20s, single authenticated identity | `111,042` rate-limited `429` responses after the first `20` successful requests |
| Full pipeline under normal conditions | `GET /api/orders`, 24 VUs, 20s, 1024 authenticated demo users, 100 ms pacing | `230.15 req/s`, `p50 2.92 ms`, `p95 6.47 ms`, `p99 11.08 ms` |

Circuit-breaker proof artifact: [`circuit-breaker-behavior.svg`](./benchmarks/results/20260406-033854-d1edb38/circuit-breaker-transition-recovery/circuit-breaker-behavior.svg), showing healthy traffic, failure-induced trip, open-circuit fast rejection, and recovery after the timeout window.

Benchmark note: the local benchmark stack sets `IRONGATE_TRUSTED_PROXIES=0.0.0.0/0,::/0` so one host can emulate many client IPs through `X-Forwarded-For`, and it enables login-claim overrides only inside the benchmark Compose stack so auth scenarios can mint distinct demo identities. Those are benchmark-only local settings. The default runtime still trusts no proxies and rejects login claim overrides unless explicitly configured.

## Further Reading

- [`ARCHITECTURE.md`](./ARCHITECTURE.md): current-runtime source of truth
- [`PROJECT_SPEC.md`](./PROJECT_SPEC.md): full project scope, success criteria, and deployment plan
- [`DESIGN_DOC.md`](./DESIGN_DOC.md): target architecture, algorithms, and failure-mode reasoning
- [`PROGRESS.md`](./PROGRESS.md): shipped phases and still-open stretch goals
- [`ADR/`](./ADR/): architectural decisions and tradeoffs
- [`benchmarks/results/20260406-033854-d1edb38/`](./benchmarks/results/20260406-033854-d1edb38/README.md): recorded benchmark evidence
