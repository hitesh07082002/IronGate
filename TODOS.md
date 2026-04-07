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
