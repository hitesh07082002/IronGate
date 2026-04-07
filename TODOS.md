# IronGate TODOs

Deferred improvements captured during planning reviews. Each entry has enough context
to be picked up 3 months later without needing to re-read the full spec.

---

## Security (from /cso audit Apr 06, 2026)

### Finding 4: SHA-pin GitHub Actions

**What:** Pin `actions/checkout@v4` and `actions/setup-go@v5` to commit SHAs in `.github/workflows/ci.yml`. Add Dependabot config to keep them current automatically.

**Why:** Tag references (`@v4`, `@v5`) are mutable — a compromised GitHub Actions source repo can push malicious code to these tags and it silently runs with write access to the repo on every CI push. SHA pinning makes this attack impossible.

**Pros:** 2-line change. Supply chain attack vector closed. Automated by Dependabot `github-actions` ecosystem.

**Cons:** None meaningful.

**Context:** Finding 4 from `/cso` audit. Both actions are first-party (GitHub's own `actions/` org) so the risk is low in practice, but SHA pinning is a best-practice with near-zero cost.

**Where to start:** `.github/workflows/ci.yml:25` — replace `@v4`/`@v5` with commit SHA. Then add `.github/dependabot.yml` with `package-ecosystem: github-actions`.

---

### Finding 6: Add Secret Scanner to CI

**What:** Add `gitleaks/gitleaks-action@v2` as a CI step in `.github/workflows/ci.yml`. Optionally add a `.gitleaks.toml` config to tune false positives.

**Why:** Phase 9 introduces `ADMIN_TOKEN`, `DEMO_TOKEN`, `DEMO_JWT`, and OTel endpoint vars. Without a scanner, accidentally committing a real secret value (e.g., setting `ADMIN_TOKEN=abc123` locally and forgetting to strip it before pushing `docker-compose.observatory.yml`) has no automated defense.

**Pros:** 4-line CI change. Catches secrets before they land on `main`. Free for public repos.

**Cons:** May produce false positives on test fixtures — manage with `.gitleaks.toml` `[[allowlists]]`.

**Context:** Finding 6 from `/cso` audit. No secret was found in git history, but Phase 9 increases the surface. Add this before M1 implementation begins.

**Where to start:** `.github/workflows/ci.yml` — add a `gitleaks-action` step after checkout. Reference the gitleaks-action README for the exact config.

---

### Finding 7: Remove payment-service host port in production

**What:** Add `ports: []` to `payment-service-1` in `deploy/docker-compose.prod.yml` to explicitly remove the `127.0.0.1:18083:8083` mapping from production.

**Why:** The host port is only needed for local debugging. In production it exposes `/chaos/down` on the VPS localhost (port 18083) with no authentication. Any process on the VPS can silently take the payment service down.

**Pros:** 2-line change. Closes the unnecessary host-level attack surface.

**Cons:** Loses convenient local debugging port in production. Use `docker exec` + `curl` instead.

**Context:** Finding 7 from `/cso` audit. Mitigated by `127.0.0.1` binding (not internet-accessible) but violates least-privilege. Fix before M6 production deploy.

**Where to start:** `deploy/docker-compose.prod.yml` — add `ports: []` under `payment-service-1`.

---

## Phase 9 Post-Stabilization

## Observatory Hardening (from M2 eng review)

### IPRateLimiter TTL eviction (P3)

**What:** Add a goroutine in `cmd/observatory/app.go` that periodically evicts `IPRateLimiter` entries older than 10 minutes so the state map does not grow without bound.

**Why:** Observatory is intentionally unauthenticated for demo traffic, so the in-memory IP limiter can see a long tail of ephemeral client IPs. Without eviction the map grows monotonically and turns a simple protection into a quiet memory leak.

**Pros:** Small change. Keeps observatory memory usage bounded during demos and public testing.

**Cons:** Adds a background cleanup loop and one more lifecycle concern to test.

**Depends on:** M5.
**Where to start:** `cmd/observatory/app.go:691` — add `lastSeen` tracking and a sweeper that prunes entries older than 10 minutes.

### Stack-scoped container labels (P3)

**What:** Add the Compose project name as a second label on managed k6 containers so cleanup only affects the current stack.

**Why:** `Runner.StopManagedContainers()` currently keys off a global managed label. If two IronGate stacks run on the same Docker host, one observatory reset can clean up the other stack’s k6 containers.

**Pros:** Safer multi-stack demos. No behavior change for the single-stack local flow.

**Cons:** Requires updating both container creation and cleanup filters together.

**Depends on:** M5.
**Where to start:** `cmd/observatory/runner.go:75` and `cmd/observatory/runner.go:177` — add a compose-project label on create, then filter on both labels during cleanup.

### Prometheus query_range param bounds (P3)

**What:** Validate `start`, `end`, `step`, and `timeout` before forwarding `query_range` requests to Prometheus, including a max 1 hour window and a minimum 1 second step.

**Why:** Observatory currently forwards these parameters raw. A malicious or accidental wide query can fan out into an expensive Prometheus scan that hurts the demo environment.

**Pros:** Prevents abusive range queries with a tight, easy-to-explain policy.

**Cons:** Adds a small amount of parameter parsing and rejection logic.

**Depends on:** M5.
**Where to start:** `cmd/observatory/metrics.go:60` — parse the query_range params, reject windows over 1 hour, and clamp or reject steps below 1 second.

### SSE concurrent stream limit (P3)

**What:** Add a maximum of 10 concurrent `/api/events` SSE streams and optionally require bearer auth for the stream endpoint.

**Why:** `/api/events` is currently unauthenticated and unlimited. A handful of long-lived connections can pin memory and goroutines in the observatory process.

**Pros:** Protects the demo plane from trivial resource exhaustion and makes the event feed safer to expose.

**Cons:** Introduces connection accounting and one more auth decision for the UI.

**Depends on:** M5.
**Where to start:** `cmd/observatory/events.go:126` — track active SSE subscriptions, reject the 11th connection, and evaluate whether the stream should share the existing bearer token flow.

### Typed Event Bus (v2 Observatory event stream)

**What:** Add `internal/events/` — a typed event bus where each middleware emits
structured events to a channel on state changes (circuit open/close, rate limit,
retry, auth fail). Observatory subscribes to this bus via an internal HTTP endpoint
instead of parsing Docker container logs.

**Why:** The current Phase 9 approach (Docker `ContainerLogs` + `stdcopy.StdCopy` +
JSON line parsing) is the highest-risk codepath in the spec. It parses logs that were
never designed as an event API. The v2 bus gives sub-millisecond event latency,
eliminates the Docker stream demux complexity, and makes the event schema explicit
rather than derived from log format. The current Phase 9 Docker log approach is the
right first step (ship fast, validate the demo), but the v2 path is the correct
long-term architecture.

**Pros:** Eliminates `events_test.go` Docker-format byte buffer testing. Explicit typed
events instead of regex parsing. Sub-millisecond latency from state change to SSE emit.
Decouples Observatory from the gateway's log format — log format changes no longer
break the event feed.

**Cons:** ~1 week of work. Requires adding a goroutine and channel to multiple
middleware packages. Changes the Observatory `GET /api/events` implementation from
Docker SDK to HTTP long-poll/SSE from the gateway itself.

**Context:** Documented in spec §8.4 ("v2 upgrade path"). Do not start until Phase 9
is stable and deployed (M6 complete). The Docker log approach works — this is
an improvement, not a fix.

**Depends on:** Phase 9 M6 complete and verified on production.
**Where to start:** `internal/events/bus.go` — define the event interface, then add
emitters to `internal/middleware/tracing.go` (circuit state changes already logged
there), `internal/transport/circuitbreaker/breaker.go`.

### Inbound Trace Propagation (Extract Path)

**What:** Add `otel.GetTextMapPropagator().Extract()` to `internal/middleware/tracing.go`
so the gateway continues inbound traces instead of always starting fresh root spans.

**Why:** When IronGate proxies traffic from another traced service (e.g., StockPulse
routing post-M6), the current implementation breaks trace continuity. The gateway
always starts a new root span and strips inbound `traceparent` headers via Engineering
Rule #2 (header sanitization).

**Pros:** Full end-to-end distributed tracing across service boundaries. StockPulse ->
IronGate -> upstream appears as one trace in Tempo.

**Cons:** Must reconcile with header sanitization: Extract `traceparent` BEFORE
stripping identity headers (`X-User-ID`, `X-User-Role`, `X-Request-ID`). ~5 lines of
code but changes the trust boundary semantics.

**Context:** Surfaced by Codex during `/plan-eng-review` (Apr 07, 2026). Not needed
for M1 observatory demo (gateway is always the trace root). Only relevant when
IronGate acts as a middle proxy for external traced traffic.

**Depends on:** M6 production deploy + StockPulse routing decision.
**Where to start:** `internal/middleware/tracing.go:33` — call `Extract()` on the
incoming request context before `tracer.Start()`, then use the extracted context as
the parent for the root span.
