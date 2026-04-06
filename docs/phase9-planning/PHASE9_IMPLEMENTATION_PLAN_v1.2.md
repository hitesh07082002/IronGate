# IronGate Phase 9 — Implementation Plan
### Chaos Observatory: Ordered Milestones, Acceptance Criteria, Risk Register

> **Source of truth:** `docs/phase9-planning/PHASE9_CHAOS_OBSERVATORY_SPEC_v2.2.md`
> **Canonical path:** `docs/phase9-planning/PHASE9_IMPLEMENTATION_PLAN_v1.2.md`
> **Implementation lock:** `docs/phase9-planning/DECISIONS_LOCK.md`
> **Appendix B decisions B1–B5:** Locked and incorporated. Not re-litigated here.
> **Plan version:** 1.2 (supersedes v1.1)
> **Date:** April 2026
>
> **Boundary:** Phase 8 on `main` is still the shipped baseline. This plan governs the
> future Phase 9 rollout and must not be read as current runtime documentation.

---

## 1. Assumptions & Dependencies

### Prerequisites (hard blockers — do not start Phase 9 until all pass)

- Phases 1–8 `main` branch passes `make test`, `make test-race`, `make coverage` (≥70%),
  and `make lint` with no errors.
- `docker-compose up` starts gateway + 3 services + Redis + Prometheus + Grafana cleanly;
  gateway serves `/health` 200 within 30 seconds.
- All Phase 8 success criteria are checked off in `PROGRESS.md`.
- A domain is owned with DNS controllable for four subdomains.
- **VPS sizing:** Local dev (M1–M5) requires a machine with ≥8 GB RAM and Docker.
  Production deployment (M6) requires an **8 GB / 4 vCPU** VPS. The VPS upgrade is
  an M6 prerequisite only — do not block M1–M5 on it.

### Appendix B decisions (locked — incorporated into spec; not re-litigated)

| ID | Decision | Spec ref |
|----|----------|---------|
| B1 | Pre-pull `grafana/k6:<pinned-tag>` in `make observatory-up`; document air-gapped procedure | §8.8, §14 |
| B2 | `allow_embedding = true` + `content_security_policy = false` in `grafana.ini` (demo VPS only — not for multi-tenant Grafana); verify cross-subdomain iframe in Chrome + Firefox on real VPS before building Observability Traces tab | §11.6, §12 M4 |
| B3 | Add `gateway_circuit_state{target="host:port"}` gauge (0=CLOSED, 1=OPEN, 2=HALF_OPEN); implement in M1, not deferred | §3.3, §8.7, §10 |
| B4 | Observatory mints its demo JWT via `POST http://gateway:8080/api/users/login`; 23h refresh ticker; fallback to `DEMO_JWT` env var | §8.8 |
| B5 | Docker SDK `ContainerLogs(Follow: true, Stdout: true)` + `stdcopy.StdCopy()` to demultiplex; one JSON object per line on gateway stdout | §8.4 |

Pinned image tags and tool versions are bumped only through an explicit PR that reruns
`make observatory-up`, the smoke path, and the demo walkthrough or replay script.

### Spec ambiguities — resolved assumptions

1. **Admin port binding.** Gateway runs a second `http.Server` on `:9090` bound to
   `0.0.0.0` within Docker. This port is **never** in `ports:` in any Compose file.
   Caddy never proxies it. Reachable only as `http://gateway:9090` from within Docker.

2. **`ADMIN_TOKEN` provisioning.** 32-byte random secret, distinct from `$DEMO_TOKEN`.
   Set as env var on both gateway and Observatory in `docker-compose.observatory.yml`.
   Observatory passes it as `Authorization: Bearer $ADMIN_TOKEN` when calling `gateway:9090`.

3. **JWT bootstrap path (B4 implementation).** Locked to
   `POST http://gateway:8080/api/users/login` with an empty body. The underlying
   service route remains `POST /users/login`, but Observatory does not call a
   `user-service-*` container directly. This avoids coupling to replica names and
   matches the repo's stable gateway-facing login contract.

4. **`Registry.Reset()` scope.** Transitions all breakers to CLOSED atomically; sets
   all `gateway_circuit_state` gauges to 0. Does not drain in-flight probe requests.
   Acceptable for demo reset purposes.

5. **Toxiproxy proxy creation.** Observatory creates the Redis proxy idempotently at
   startup via `PUT /proxies/redis`. If the proxy already exists (Observatory restart),
   PUT succeeds without error.

6. **S6 "300s cap" behavior.** k6 runs for the full 300s. Chaos fires at T+10s. The
   CB self-heals well before 300s. Scenario ends when user hits Stop or 300s elapses.

7. **Docker Compose network name.** k6 containers use `NetworkMode` set to
   `<COMPOSE_PROJECT_NAME>_default`. `COMPOSE_PROJECT_NAME` read from Observatory
   environment (default: `irongate`). Documented in `docker-compose.observatory.yml` comments.

8. **Rate limit separation (§11.1).** Two distinct limits:
   - **60 req/min per IP** on `GET /api/metrics/query` and `/query_range` only (metrics proxy — higher risk)
   - **100 req/min per IP** on all Observatory endpoints (global floor)
   A client hitting the metrics proxy at 61 req/min triggers the 60/min limit first,
   before the 100/min global limit. These are two separate token buckets.

9. **Redis address override mechanism.** The gateway remains YAML-first. Phase 9 changes
   `configs/gateway.yaml` to resolve Redis as `address: "${REDIS_ADDR:-redis:6379}"`,
   and `docker-compose.observatory.yml` sets `REDIS_ADDR=toxiproxy:6380`.

---

## 2. Work Breakdown

### Milestone Dependency Graph

```
M1 (OTel + Exemplars + Metrics)
  └─► M2 (Observatory Backend Core)
         ├─► M3 (All 9 Scenarios + k6)  ──────────────────────┐
         └─► M4 (Frontend — 4 pages)    ──────────────────────┤
               └─► M5 (Hardening + Dashboards 2–4)            │
                     └─► M6 (Deploy + Demo Video)   ◄──────────┘
```

M3 and M4 both depend on M2 but are independent of each other — run in parallel.
M5 needs both M3 and M4. M6 needs M5.

**Recommended vertical slice before full build (before completing M2):**

After M1 exits but before building all nine scenarios, validate the full observability
chain end-to-end:
1. `make observatory-up` → send one request → trace appears in Grafana Tempo
2. One exemplar dot → click → trace opens in same Grafana tab
3. One SSE event with `trace_id` appears in `GET /api/events` stream
4. One `POST /api/reset` → `{"status":"clean"}`

This catches infrastructure problems (Collector config, Grafana provisioning, Docker
networking, Docker stream multiplexing) before they contaminate scenario testing.

---

### M1 — OTel, Exemplars, Metrics (Weeks 1–2)

**Goal:** Every gateway request produces a complete OTel trace in Tempo; exemplar dot
on Grafana p99 latency panel links to that trace; `gateway_circuit_state` gauge and
admin reset endpoint are live.

**Depends on:** None (Phase 1–8 prerequisites satisfied).

**Deliverables:**

| Path | Description |
|------|-------------|
| `internal/telemetry/telemetry.go` | OTel `Init()`; no-op fallback; 5s OTLP connection timeout; 10s export timeout |
| `internal/transport/circuitbreaker/` | `Registry.Reset()` + `gateway_circuit_state` gauge updates on every state transition |
| `cmd/gateway/main.go` | Second HTTP server on `:9090`; OTel init; admin endpoint; `ObserveWithExemplar` wired |
| `otel/collector-config.yaml` | Collector config per §5.6 (`memory_limiter` before `batch`) |
| `docker-compose.observatory.yml` | Skeleton: `tempo`, `otel-collector` containers; gateway OTel env vars; `ADMIN_TOKEN`; pinned image tags |
| `configs/gateway.yaml` | Redis address becomes `${REDIS_ADDR:-redis:6379}` so the observatory overlay can route through Toxiproxy without a second config file |
| `monitoring/grafana/provisioning/grafana.ini` | `allow_embedding = true`, `content_security_policy = false` |
| `monitoring/grafana/provisioning/datasources/tempo.yaml` | Tempo datasource with `uid: tempo`; no Loki fields |
| `monitoring/grafana/provisioning/datasources/prometheus.yaml` | Updated: `exemplarTraceIdDestinations` pointing to `uid: tempo` |
| `monitoring/prometheus/prometheus.yml` | `exemplar_storage: true`; `scrape_protocols` for OpenMetrics |
| `monitoring/grafana/dashboards/gateway-overview.json` | Updated: p99 latency panel with exemplars enabled |

**Sub-milestones:**

**M1.1 — Telemetry package + no-op wiring (2–3 days)**

Deliverables:
- `internal/telemetry/telemetry.go`: `Init()` returning `TracerProvider` + shutdown func.
  No-op when `OTEL_EXPORTER_OTLP_ENDPOINT` unset. OTLP gRPC exporter with 5s dial
  timeout and 10s per-export timeout. Log a single warning on first failed export; do
  not log on every subsequent failure (prevents log spam in local dev without Collector).
- All existing middleware and transport constructors accept an optional `trace.Tracer`
  parameter (nil = no-op, no instrumentation).
- `make test` and `make test-race` pass unchanged — no existing test touches OTel.

Acceptance:
- `OTEL_EXPORTER_OTLP_ENDPOINT` unset → `make test` green; `curl localhost:8080/health` → 200.
- `OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317` set, no Collector running → gateway starts;
  logs exactly one connection warning within the first 10s; continues serving requests normally;
  no crash; no panic; test suite still passes.
- Verify no goroutine leak: `pprof` goroutine count stable after 30s with unreachable Collector.

**M1.2 — Outer chain span instrumentation (2–3 days)**

Deliverables:
- Spans: `irongate.middleware.tracing`, `irongate.middleware.router`,
  `irongate.middleware.auth`, `irongate.middleware.ratelimiter`.
- All §5.4 outer-chain attributes set per contract.
- `auth.user_id`: `hashAttr(jwt_sub_claim)` — first 8 hex chars of SHA-256.
- `ratelimit.client_key`: `hashAttr(user_id_or_ip)`.
- `http.path` on root span: `route.Path` from matched `RouteConfig` (e.g. `/api/users`),
  never `req.URL.Path` (e.g. `/api/users/42`).

Acceptance:
- With Tempo + OTel Collector running via overlay: send `GET /api/users` with valid JWT
  → Grafana Explore (Tempo datasource) shows root span + four outer-chain child spans
  matching §5.3 waterfall structure.
- `irongate.middleware.tracing` span has `http.path=/api/users` (route template, not `/api/users/<id>`).
- Send request with missing JWT → `auth.outcome=failed` on auth span; no `irongate.proxy` span exists.
- `auth.user_id` and `ratelimit.client_key` attributes are 8-character hex strings, not raw values.
- `make test-race` green.

**M1.3 — Inner chain span instrumentation (2–3 days)**

Deliverables:
- Spans: `irongate.transport.retry.attempt`, `irongate.transport.retry.backoff`,
  `irongate.transport.loadbalancer`, `irongate.transport.circuitbreaker`, `irongate.transport.upstream`.
- All §5.4 inner-chain attributes set per contract.
- Open circuit: `irongate.transport.circuitbreaker` span has `cb.state=open`, STATUS=ERROR,
  span event `circuit_rejected`. No `irongate.transport.upstream` child span.

Acceptance:
- Kill `user-service-2`; send 10 requests → Grafana Explore shows at least one trace with
  `cb.state=open`, zero upstream span child, root span duration < 5ms.
- Run `/chaos/errors` at 50% on one instance; send 20 requests → at least one trace shows
  `retry.attempt=1` (503) → `retry.backoff` → `retry.attempt=2` (200, different target).
- `make test-race` green.

**M1.4 — `gateway_circuit_state` gauge + admin reset endpoint (1 day)**

Deliverables:
- `gateway_circuit_state{target="host:port"}` gauge; values 0/1/2; updated on every CB
  state transition via `Registry`.
- `Registry.Reset()`: transitions all breakers to CLOSED atomically; sets all gauges to 0.
- Second HTTP server on `:9090` in `cmd/gateway/main.go`; not in Compose `ports:`.
- `POST /admin/circuit-breakers/reset`: validates `Authorization: Bearer $ADMIN_TOKEN`
  via `hmac.Equal`; calls `Registry.Reset()`; returns `{"reset":true,"targets_cleared":N}`.
  Returns 401 on missing or invalid token.

Acceptance:
- From within Docker network: `curl http://gateway:9090/admin/circuit-breakers/reset -XPOST -H "Authorization: Bearer $ADMIN_TOKEN"` → 200 `{"reset":true,"targets_cleared":4}`.
- From VPS host: `curl localhost:9090/admin/circuit-breakers/reset` → connection refused.
- Trip a circuit (kill service; send requests until CB opens); call reset; `curl "prometheus:9090/api/v1/query?query=gateway_circuit_state"` → all values 0 within 1 second.
- Call reset without token → 401.

**M1.5 — Prometheus exemplars + Grafana correlation (2 days)**

Deliverables:
- Tracing middleware uses `ObserveWithExemplar()` with `traceID` label when `spanCtx.IsSampled()` (§6.2 Step 2).
- `monitoring/prometheus/prometheus.yml`: `exemplar_storage: true`; `scrape_protocols` with `OpenMetricsText1.0.0` (§6.2 Step 3).
- `monitoring/grafana/provisioning/datasources/prometheus.yaml`: `exemplarTraceIdDestinations` pointing to `datasourceUid: tempo` (§6.2 Step 4).
- `monitoring/grafana/provisioning/grafana.ini`: `allow_embedding = true`, `content_security_policy = false`.
- `docker-compose.observatory.yml`: Tempo + OTel Collector containers; `OTEL_EXPORTER_OTLP_ENDPOINT=otel-collector:4317`; `OTEL_TRACES_SAMPLER=always_on`; `ADMIN_TOKEN`; pinned image tags.
- Dashboard 1 p99 panel: exemplars enabled.

Acceptance:
- `make observatory-up` → all containers healthy.
- Run 30s of load.
- Grafana: Gateway Overview dashboard → p99 latency panel → ≥1 exemplar dot visible within 30s window.
- Click exemplar dot → Grafana Explore opens with Tempo trace; trace contains full outer + inner span waterfall from §5.3.
- `curl -H "Accept: application/openmetrics-text" http://localhost:8080/metrics | grep "# {traceID"` → ≥1 exemplar annotation.
- The `traceID` in the exemplar annotation matches the `traceID` in the Tempo trace that opens on click (end-to-end verification).

**M1 Acceptance (exit criterion):**
- All sub-milestone criteria pass.
- Money trace verified: kill `user-service-2`; send one request; Grafana Explore shows `irongate.transport.circuitbreaker` with `cb.state=open`, no upstream child, root duration < 5ms.
- `make test`, `make test-race`, `make lint` all green with OTel wired in.
- **Hard gate: do not begin M2 until the exemplar dot → Tempo trace link is confirmed end-to-end on a running stack.**

**Out of scope for M1:** Observatory service, k6 scripts, frontend, scenarios, all reset
logic, Toxiproxy, Grafana Explore embedding, Dashboards 2–4.

---

### M2 — Observatory Backend Core (Week 3)

**Goal:** Observatory runs in Docker, executes happy-path and circuit-breaker-recovery
via API, streams events via Docker SDK log stream, resets cleanly.

**Depends on:** M1 fully complete.

**Deliverables:**

| Path | Description |
|------|-------------|
| `cmd/observatory/main.go` | HTTP server on `:9000`; health with `spec_version`; startup JWT fetch (B4); Toxiproxy init; graceful shutdown |
| `cmd/observatory/api.go` | All route handlers per §8.2 |
| `cmd/observatory/scenarios.go` | YAML loader; schema validation; cap enforcement (RPS ≤ 500, duration ≤ 300s) |
| `cmd/observatory/runner.go` | k6 Docker runner: pre-pull (B1); explicit network mode (R4); `COMPOSE_PROJECT_NAME` env |
| `cmd/observatory/chaos.go` | Chaos sequence execution at `at_seconds` offsets; HTTP to service chaos endpoints |
| `cmd/observatory/events.go` | `ContainerLogs` + `stdcopy.StdCopy()` (B5); JSON parse; sampling per §8.4; SSE broadcast with per-client buffered channel; EOF reconnect |
| `cmd/observatory/events_test.go` | **Required:** Unit test for Docker multiplexed stream demux (bytes.Buffer with 8-byte headers) |
| `cmd/observatory/metrics.go` | Prometheus proxy: 60/min metrics rate limit + 100/min global; 10s timeout; allowlist from §8.7 |
| `cmd/observatory/reset.go` | Full reset per §8.5; 30s timeout; partial failure response |
| `cmd/observatory/toxiproxy.go` | Toxiproxy API client; idempotent `PUT /proxies/redis` at startup |
| `scenarios/happy-path.yaml` | S1 definition |
| `scenarios/circuit-breaker-recovery.yaml` | S6 definition |
| `scenarios/k6/lib/common.js` | Shared helpers: base URL, auth header |
| `scenarios/k6/happy-path.js` | S1 k6 script with `setup()` gateway health check |
| `scenarios/k6/circuit-breaker-recovery.js` | S6 k6 script with `setup()` gateway health check |
| `docker-compose.observatory.yml` | Updated: `observatory`, `toxiproxy` containers; `REDIS_ADDR=toxiproxy:6380` on gateway; `DEMO_JWT`, `DEMO_TOKEN`, `ADMIN_TOKEN` env vars |

**Sub-milestones:**

**M2.1 — Observatory skeleton + JWT startup (1 day)**

Deliverables:
- HTTP server; graceful shutdown; `GET /api/health` → `{"status":"ok","spec_version":"2.2","jwt_valid":true,"toxiproxy_ready":true}`.
- JWT startup (B4): calls `POST http://gateway:8080/api/users/login` with an empty
  body; stores token; starts 23h refresh ticker. Falls back to `DEMO_JWT` env var if set.
  Phase 9 does not introduce a separate `DEMO_USER` / `DEMO_PASS` contract. Exits with
  non-zero code if neither path succeeds.

Acceptance:
- `docker-compose -f docker-compose.yml -f docker-compose.observatory.yml up observatory` → health endpoint returns correct JSON including `jwt_valid: true`.
- Set `DEMO_JWT=<valid_token>` → Observatory starts without calling login endpoint; logs "Using static DEMO_JWT".
- Unset `DEMO_JWT` and make the gateway login route unavailable → Observatory logs bootstrap error and exits non-zero within 10 seconds.

**M2.2 — Scenario loader + API endpoints (1–2 days)**

Deliverables:
- YAML loader validates schema and enforces caps at load time.
- `GET /api/scenarios` → JSON array with `name`, `display_name`, `category`, `intensity_options`, `duration_options`.
- `GET /api/scenarios/:name` → single scenario JSON; 404 on unknown.
- `GET /api/scenarios/:name/status` → `{"status":"idle|running|stopping|error"}`.

Acceptance:
- `curl observatory:9000/api/scenarios` → JSON array with ≥2 entries (the two loaded in M2).
- `curl observatory:9000/api/scenarios/does-not-exist` → 404 `{"error":"scenario not found","code":404}`.
- A scenario YAML with any intensity `rps: 600` → Observatory logs validation error at startup and marks that scenario invalid (does not load it; does not crash on other scenarios).

**M2.3 — k6 Docker runner (2 days)**

Deliverables:
- Pre-pulls `grafana/k6:<pinned-tag>` if not present (B1).
- Creates k6 container with `NetworkMode: <COMPOSE_PROJECT_NAME>_default` (R4).
- Passes env vars: `RPS`, `DURATION`, `TARGET_URL=http://gateway:8080`, `JWT=<demo_jwt>`.
- Mounts `./scenarios/k6:/scripts:ro`; runs `run /scripts/<scenario>.js`.
- Streams container stdout to Observatory log; stores container ID; enforces one concurrent scenario (409 if running).
- `POST /api/scenarios/:name/run` → validates params, caps, starts k6, returns `{"status":"started"}`.
- `POST /api/scenarios/:name/stop` → stops k6 container.

Acceptance:
- `curl -XPOST -H "Authorization: Bearer $DEMO_TOKEN" observatory:9000/api/scenarios/happy-path/run -d '{"intensity":"mild","duration":30}'` → 200 `{"status":"started"}`; `docker ps` shows a k6 container.
- While running: second POST to any `/run` → 409 Conflict.
- After 30s: k6 container exits; `GET /api/scenarios/happy-path/status` → `{"status":"idle"}`.
- `curl "prometheus:9090/api/v1/query?query=gateway_requests_total"` → increasing value during k6 run (confirms k6 reaches gateway through Docker network — primary R4 check).

**M2.4 — Chaos orchestration (1–2 days)**

Deliverables:
- Goroutine reads `chaos_sequence` from scenario YAML; sleeps until `at_seconds`; calls
  `http://<service>:<port>/chaos/down` or `/chaos/reset` on the target service hostname.
- Chaos goroutine starts with k6 on `/run`; cancelled on `/stop`.

Acceptance:
- Run circuit-breaker-recovery at Mild; 12 seconds in: `curl http://user-service-2:8081/health` from within Docker network → connection refused (service is down).
- Call `POST /stop`: chaos goroutine exits within 2 seconds.
- `gateway_open_circuits` metric increases within 20 seconds of `service_down` action (verify via Prometheus query).

**M2.5 — Docker SDK log stream → SSE event feed (2–3 days)**

Deliverables:
- `cmd/observatory/events.go`: `ContainerLogs(Follow: true, Stdout: true, Stderr: false)` (B5).
- **Critical:** `stdcopy.StdCopy(lineWriter, io.Discard, logStream)` to demultiplex 8-byte Docker headers before line parsing.
- JSON line parsing; sampling per §8.4; SSE broadcast to per-client buffered channel (size 256); drop oldest on full.
- On EOF (gateway restart): reconnect with 1s → 2s → 5s → 30s cap exponential backoff.
- Sanitize before emit: strip JWT-pattern strings (`^eyJ...`); strip `$ADMIN_TOKEN` and `$DEMO_TOKEN` exact matches.
- Prune events older than 5 minutes from in-memory buffer.
- `GET /api/events` → `Content-Type: text/event-stream`; each event as `data: <json>\n\n`.
- `cmd/observatory/events_test.go`: **must exist and pass** — unit test constructs a `bytes.Buffer` with Docker-format 8-byte headers prepended to real JSON log lines; asserts that `parseEvents()` produces correct output. This test is the primary mitigation for R2.

Acceptance:
- `curl -N observatory:9000/api/events` → chunked response begins; stays connected for 60s idle.
- Run happy-path at Severe (300 RPS): `request_success` events appear within 10 seconds (1% of 300 RPS ≈ 3 events/s).
- Kill `user-service-2` during circuit-breaker-recovery scenario: `circuit_open` event appears in stream within 3 seconds.
- `circuit_open` event JSON has `attrs.trace_id` as a 32-character hex string.
- 5 simultaneous SSE connections during Severe scenario: Observatory does not crash; `docker stats observatory` shows stable memory.
- Stop all scenarios; 5 minutes idle: stream still connected.
- **`events_test.go` passes:** `go test ./cmd/observatory/... -run TestDockerStreamParse` exits 0.

**M2.6 — Prometheus proxy + reset + Toxiproxy init (2 days)**

Deliverables:
- `cmd/observatory/metrics.go`:
  - Two separate token buckets: 60/min per IP for `/api/metrics/query` and `/query_range`; 100/min per IP for all endpoints globally.
  - 10s timeout per Prometheus request.
  - Allowlist from §8.7 (including `gateway_circuit_state`).
- `cmd/observatory/toxiproxy.go`: idempotent `PUT /proxies/redis` at startup.
- `cmd/observatory/reset.go`: §8.5 sequence; 30s timeout; partial failure response.
- `POST /api/reset` handler.

Acceptance:
- `curl "observatory:9000/api/metrics/query?query=gateway_requests_total"` → valid Prometheus JSON.
- `curl "observatory:9000/api/metrics/query?query=gateway_circuit_state"` → valid response (confirms `gateway_circuit_state` in allowlist).
- `curl "observatory:9000/api/metrics/query?query=up{job='evil'}"` → 403 `{"error":"query not permitted","code":403}`.
- **60/min metrics limit:** Send 61 allowlisted queries to `/api/metrics/query` within 60s from same IP → 61st returns 429.
- **100/min global limit:** Send 101 requests to `/api/scenarios` (non-metrics) within 60s from same IP → 101st returns 429.
- After circuit-breaker-recovery at Moderate (60s): `POST /api/reset` → `{"status":"clean"}` within 15s.
- After reset: `gateway_circuit_state` all 0 (via proxy query); Redis `SCAN 0 MATCH rate_limit:*` → empty; all 5 services return 200 on `/health`.
- Toxiproxy on Observatory startup: `curl toxiproxy:8474/proxies` → shows `redis` proxy. Restart Observatory → still shows `redis` proxy (idempotency).

**M2 Acceptance (exit criterion):**

```bash
# Run end-to-end
curl -XPOST -H "Authorization: Bearer $DEMO_TOKEN" \
  observatory:9000/api/scenarios/circuit-breaker-recovery/run \
  -d '{"intensity":"moderate","duration":60}'

# Monitor SSE in another terminal
curl -N observatory:9000/api/events
# Must see within 60s:
#   {"type":"scenario_started",...}
#   {"type":"circuit_open",...}      ← within ~15s of start
#   {"type":"circuit_closed",...}    ← automatic recovery

# Check status after completion
curl observatory:9000/api/scenarios/circuit-breaker-recovery/status
# → {"status":"idle"}

# Reset (run 3 times)
for i in 1 2 3; do
  curl -XPOST -H "Authorization: Bearer $DEMO_TOKEN" observatory:9000/api/reset
  # → {"status":"clean"} each time, within 15s
done
```

- `make test`, `make test-race`, `make lint` green. Observatory unit tests pass:
  YAML loader validation, cap enforcement, allowlist matching, JWT fallback, Docker stream demux.

**Out of scope for M2:** Remaining 7 scenarios, frontend, Dashboards 2–4, production TLS.

---

### M3 — All 9 Scenarios + k6 Scripts (Week 4)

**Goal:** All 9 scenario YAMLs and k6 scripts complete; each runs and resets cleanly.

**Depends on:** M2 fully complete.

**Deliverables:** All remaining scenario files (S2, S3+variant, S4, S5, S7, S8, S9).
S1 and S6 were delivered in M2.

**Sub-milestones:**

**M3.1 — S2 Auth Wall + S3 Rate Limit Storm (1–2 days)**

Acceptance (all numbers assume default scenario YAML values — tag k6 scripts with this assumption):

**S2 Auth Wall** (missing JWT, Severe 500 RPS, 30s):
- `gateway_request_failures_total` increases at approximately 500 req/s.
- `sum(rate(gateway_requests_total{service="user-service"}[30s]))` via Observatory proxy ≈ 0 (no upstream traffic — auth rejects before proxy).
- SSE shows `auth_failed` events.

**S3 Rate Limit Storm** (50 req/min threshold, 200 RPS single key, 60s) — *given default scenario YAML*:
- `gateway_rate_limit_rejections_total` increases.
- Upstream RPS ≈ 0.83/s (50 req/min = 0.833/s) — *given 50 req/min default*.
- `curl -v gateway:8080/api/users -H "Authorization: Bearer $JWT"` → `X-RateLimit-Remaining: 0` and `Retry-After` header visible after threshold crossed.

**S3 Many-Keys variant** (50 concurrent users, same 50 req/min threshold each) — *given defaults*:
- No single user receives 429; `gateway_rate_limit_rejections_total` stays near zero.
- Total upstream RPS ≈ 50 × 0.833 ≈ 41/s — *given defaults*.

**M3.2 — S4 Single Replica Death + S5 Upstream 5xx Retry (1–2 days)**

Acceptance:

**S4 Single Replica Death** (kill user-service-2, Moderate 100 RPS, 120s) — *given default gateway.yaml CB thresholds*:
- After T+10s chaos action: SSE shows no events with `X-Served-By: user-service-2`; exclusively `X-Served-By: user-service-1`.
- `gateway_circuit_state{target="user-service-2:8092"}` = 1 within 30s — *given default CB config*.
- Client error rate < 1% after CB opens (retries absorb the failure window).
- Reset restores `gateway_circuit_state` to 0 for all targets.

**S5 at 30% error rate** (100 RPS, 60s):
- `gateway_retries_total` > 0.
- Client error rate < 10% (retries absorb most failures).
- At least one Tempo trace shows two `irongate.transport.retry.attempt` spans under one root.

**S5 at 90% error rate** (100 RPS, 60s):
- `gateway_circuit_opens_total` increases; SSE shows `circuit_open`; client 503 rate > 0.

**M3.3 — S7 Cascading Failure + S8 Redis Impaired (1–2 days)**

Acceptance:

**S7 Cascading Failure** (kill both user-service instances, 60s):
- After T+5s: `gateway_circuit_state` for instance 1 = 1; traffic visible on instance 2.
- After T+20s: `gateway_circuit_state` for instance 2 = 1; SSE shows `all_targets_exhausted`; `curl gateway:8080/api/users -H "Authorization: Bearer $JWT"` → 503 `{"error":"no healthy targets..."}`.
- After reset: both gauges = 0; `/api/users` → 200.

**S8 Redis Impaired** (full blackout via Toxiproxy, 60s):
- `gateway_rate_limit_rejections_total` drops to zero during blackout (fail-open engaged).
- SSE shows `redis_unavailable` warning events.
- `curl -v gateway:8080/api/users -H "Authorization: Bearer $JWT"` → no `X-RateLimit-*` headers.
- After reset (toxic removed): rate limiting resumes; `X-RateLimit-Remaining` reappears.

**M3.4 — S9 Latency Injection + sequential all-scenarios run (1 day)**

Acceptance:

**S9 Latency Injection** (2000ms injected, route timeout 500ms, 50 RPS, 60s):
- `gateway_request_duration_seconds` p99 climbs to ~2000ms for the injected target.
- 504 responses appear; `gateway_retries_total` increases.
- `gateway_in_flight_requests` stays bounded (< 3× normal for this RPS).
- Tempo trace: `upstream.duration_ms ≈ 2000` + `deadline_exceeded` on attempt 1; second attempt < 50ms on different target.

**Sequential all-scenarios run:**
- Run S1 → S2 → S3 → S4 → S5 → S6 → S7 → S8 → S9 with `POST /api/reset` between each.
- All 9 produce expected signals per §7.2.
- All 9 reset clean within 15 seconds.
- `make scenario-list` prints all 9 names.

**M3 Acceptance (exit criterion):**
- All 9 scenarios produce expected signals and clean reset.
- 10 consecutive resets (no scenario between) all return `{"status":"clean"}` within 15s.

**Out of scope for M3:** Frontend, Dashboards 2–4, production deployment.

---

### M4 — Frontend: All Four Pages (Weeks 5–6)

**Goal:** React frontend complete at `web:3001`; all 4 pages functional against Observatory and Grafana.

**Depends on:** M2 API stable (M2.2 minimum); does not require M3. Runs in parallel with M3.

**Sub-milestones:**

**M4.1 — Scaffold + design system (1 day)**

Deliverables:
- Vite + React + TypeScript (strict) + Tailwind (IronGate tokens from §9.1) + shadcn primitives
  copied into `web/src/components/ui/` + `cn()` in `web/src/lib/utils.ts`.
- All 9 shadcn primitives copied: alert-dialog, badge, button, dialog, select, separator, sheet, tabs, tooltip.
- React Router: four routes (`/`, `/about`, `/chaos`, `/observability`).
- Shared nav component; placeholder page content.
- `package.json` with exact semver pins (not `^` or `~`).

Acceptance:
- `make web-dev` → Vite on `:5173`; all four routes render with placeholder content.
- `make web-lint` passes.
- `bg-ig-bg` on body; `font-mono` on a test element; color tokens resolve in browser DevTools.

**M4.2 — Landing page (1–2 days)**

Deliverables:
- `hooks/usePrometheusQuery.ts`: polls Observatory `/api/metrics/query` every 2s.
- Static SVG pipeline diagram (animation deferred to M4.7).
- Three stat counters (5s poll): `gateway_requests_total`, `gateway_circuit_opens_total`, `gateway_rate_limit_rejections_total`.
- "Launch Observatory →" CTA; nav complete.

Acceptance:
- Stat counters update every 5s; one counter value matches `curl observatory:9000/api/metrics/query?query=gateway_requests_total` result.
- `make web-build` → `dist/` with zero TypeScript errors.
- At 375px: counters stack vertically; CTA fully visible; no horizontal scroll.
- At ≥1280px: hero diagram centered; stat strip visible below.

**M4.3 — About page (1–2 days)**

Deliverables:
- Horizontal pipeline diagram; 8 clickable nodes; shadcn `Sheet` drawers per node.
- Each drawer: what-it-does, why-it's-here, failure mode, ADR link.
- ADR decision card grid (2×4).

Acceptance:
- Click each of the 8 nodes → correct drawer opens with non-placeholder content.
- ADR cards link to valid GitHub URLs (verify 2 of 8 manually).
- At 1024–1279px: pipeline renders without horizontal overflow.
- At <1024px: pipeline renders as vertical list; drawers still open.

**M4.4 — Observatory page: controls + system status (2–3 days)**

Deliverables:
- `hooks/useScenario.ts`, `hooks/useSystemStatus.ts`.
- Scenario picker (9 cards), intensity/duration buttons, "what to watch" callout, expected signals.
- Run controls: RUN (green), STOP (yellow), RESET ALL (red + `AlertDialog`).
- RUN disabled during reset; re-enables only on `{"status":"clean"}`.
- System status: 6-dot grid (Gateway, Redis, 4 services); 3s poll.
- Active scenario banner replaces picker when running.

Acceptance:
- 9 scenario cards render from Observatory API.
- Select S6, Severe, 120s → click RUN → POST fires with `{"intensity":"severe","duration":120}`; banner appears with elapsed timer.
- RESET ALL → `AlertDialog` appears; cancel = no action; confirm = POST `/api/reset`.
- Kill `user-service-2` externally → its dot turns red within 5 seconds.
- During reset: RUN disabled. After `{"status":"clean"}`: RUN re-enables.

**M4.5 — Observatory page: event feed + metric panels + trace shortcuts (2–3 days)**

**Pre-condition (B2 check — do this before M4.6):** Open Grafana at `grafana.yourdomain.com`
in an iframe from `demo.yourdomain.com` in Chrome and Firefox. If embedding is blocked
despite `allow_embedding = true` and `content_security_policy = false`, implement the
"Open in Grafana →" fallback for the Traces tab (§9.5) and document the decision. Do not
build M4.6 Tab 2 on an assumption — verify first.

Deliverables:
- `hooks/useEventStream.ts`: `EventSource` + auto-reconnect (1s → 2s → 5s → 30s cap).
- `EventFeed`: reverse-chronological; color-coded per §8.4 CSS tokens; filter bar; collapse
  high-frequency same-type events; auto-scroll with pause; "↓ Jump to latest" button;
  `[View Trace →]` links on events with `trace_id`; 5-minute event pruning.
- Request counter strip: ✅ success / ❌ error / 🔄 retry / ⛔ rate-limited. 1s poll.
- Four Recharts panels (2s poll via Observatory proxy):
  - RequestRatePanel, LatencyPanel, CircuitStateTimeline, RateLimitPanel.
  - CircuitStateTimeline: queries `gateway_circuit_state{target=~".+"}` as range query;
    renders one colored row per target (0=green, 1=red, 2=yellow).
- TraceShortcutsBar: 3 most recent traces from SSE events with `trace_id`.

Acceptance:
- Run S6 at Moderate: `circuit_open` event appears in feed with `ev-error` background within 3 seconds of `service_down` chaos action.
- Click `[View Trace →]` on `circuit_open` event → new tab opens Grafana Explore URL containing the 32-character `trace_id` from the SSE event JSON.
- CircuitStateTimeline: `user-service-2` row turns red within 3 seconds of `service_down` (derived from `gateway_circuit_state` metric = 1).
- RateLimitPanel: run S3 at Severe → red rejected stack ≥50% of bar within 30 seconds.
- SSE disconnects: "Reconnecting..." indicator appears; reconnects within 30s after Observatory restarts.
- At 300 RPS (S6 Severe): browser CPU < 20% sustained in DevTools; no UI freezing.

**M4.6 — Observability page (2 days)**

Deliverables:
- shadcn `Tabs`: Metrics / Traces / Logs.
- **Metrics tab:** Four Grafana `d-solo` iframes (2×2 grid); time-range picker updates all four src URLs; "Open full Grafana →" link.
- **Traces tab:** Grafana Explore iframe (Tempo datasource, `{service.name="irongate"}`) or fallback button (if B2 embedding was blocked in M4.5 pre-condition). Scenario trace shortcuts populated from Observatory in-memory recent traces.
- **Logs tab:** SSE stream from `/api/events`; Level/Type filters; client-side substring search; ERROR=red, WARN=yellow; `req=<uuid>` clickable → Grafana Explore TraceQL search.

Acceptance:
- **Metrics tab:** All four iframes load and display live data; time picker change updates all four src URLs.
- **Traces tab (if embedding works):** Iframe loads Grafana Explore with Tempo datasource; after running S6, scenario shortcuts show ≥2 trace links; clicking one opens correct trace.
- **Traces tab (if fallback):** "Open Traces in Grafana →" button opens `grafana.yourdomain.com/explore` in new tab.
- **Logs tab:** Events stream while S6 runs; `circuit_open` events highlighted; search "circuit" → only circuit-type events visible; click `req=<uuid>` → Grafana URL contains TraceQL query for that request_id.

**M4.7 — Pipeline animation + Dockerfile (1–2 days)**

Deliverables:
- Prometheus-driven dot animation on Landing SVG: dots appear at `gateway_in_flight_requests > 0`; speed proportional to RPS; color proportional to error rate.
- `web/Dockerfile`: multi-stage `npm run build` → nginx alpine serving `/dist`.

Acceptance:
- With 100 RPS load running: animated dots move through pipeline.
- At 0 RPS: animation pauses.
- `docker build web/` succeeds; `curl localhost:3001` → 200 HTML.
- `make web-build` → zero TypeScript errors.
- At <1024px: "Best viewed on desktop" banner on Observatory and Observability pages; single-column layout; metric panels behind "Show Metrics" toggle.

**M4 Acceptance (exit criterion):**
- A stranger opens Observatory, picks S6 at Severe, hits RUN, follows all 8 steps from §1 in under 90 seconds without instructions.
- All four pages render at 1280px without layout breakage.
- `make web-build` and `make web-lint` green.

**Out of scope for M4:** Dashboards 2–4, production TLS, demo video.

---

### M5 — Hardening, Security, and Dashboards 2–4 (Week 7)

**Goal:** All security requirements enforced; all 4 dashboards provisioned; 10 consecutive resets pass.

**Depends on:** M3 and M4 both complete.

**Deliverables:**

| Path | Description |
|------|-------------|
| `grafana/dashboards/resilience.json` | Dashboard 2 |
| `grafana/dashboards/rate-limiting.json` | Dashboard 3 |
| `grafana/dashboards/scenario-view.json` | Dashboard 4 |
| Updated `docker-compose.observatory.yml` | `mem_limit`, `cpus` on observatory, web, toxiproxy |

**Sub-milestones:**

**M5.1 — Grafana Dashboards 2–4 (2 days)**

Acceptance:
- `make observatory-up` → Grafana dashboards menu → all four dashboards present; no manual import.
- **Dashboard 2 during S6:** `gateway_circuit_state` state timeline shows 0→1→2→0 for `user-service-2:8092`; retry counter climbs; upstream picks show redistribution.
- **Dashboard 3 during S3:** rejected stack dominates allowed/rejected bar; Redis latency panel shows data > 0.
- **Dashboard 4:** All four panels render; `$scenario` variable in panel titles; legible at 1080p when screen-shared.

**M5.2 — Security hardening (1–2 days)**

Acceptance (all must pass):
- `curl -XPOST observatory:9000/api/scenarios/happy-path/run -d '{"intensity":"mild","duration":30}'` (no auth) → 401.
- `curl -XPOST -H "Authorization: Bearer wrongtoken" observatory:9000/api/reset` → 401.
- **60/min metrics rate limit:** 61 allowlisted `GET /api/metrics/query` requests from same IP within 60s → 61st returns 429. *(Tests §8.7 60/min limit specifically.)*
- **100/min global rate limit:** 101 `GET /api/scenarios` requests from same IP within 60s → 101st returns 429. *(Tests §11.2 100/min global limit specifically.)*
- Run S2 (Auth Wall, Severe): inspect SSE stream → no `eyJ...` JWT-pattern string in any `attrs` value.
- `curl localhost:9090/admin/circuit-breakers/reset` from VPS host → connection refused.
- `docker stats observatory` during S6 Severe: memory within configured `mem_limit`.

**M5.3 — Sequential run + reset stress test (1 day)**

Acceptance:
- All 9 scenarios run sequentially with reset between each; all produce expected signals; all reset clean.
- 10 consecutive resets (no scenario between): all return `{"status":"clean"}` within 15 seconds.
- `make observatory-up` on fresh Docker environment: full stack healthy within 90 seconds.
- `make lint`, `make test`, `make test-race` green.

**M5 Acceptance (exit criterion):** All dashboards provisioned; all security tests pass; 10 consecutive resets pass; all make targets green.

**Out of scope for M5:** VPS, TLS, Caddy, demo video.

---

### M6 — Production Deployment + Demo Video (Weeks 8–9)

**Goal:** Full stack live at 4 HTTPS subdomains on production VPS; 2-minute demo video recorded; all §17 criteria checked.

**Depends on:** M5 fully complete.

**Deliverables:** Updated `deploy/Caddyfile.template`, `scripts/bootstrap-production-host.sh`, `scripts/deploy-production.sh`, demo video, updated `README.md`, `PROGRESS.md` fully checked.

**Sub-milestones:**

**M6.1 — VPS setup + stack deployment (1–2 days)**

Acceptance:
- VPS: `free -h` shows ≥7.5 GB; `nproc` shows ≥4.
- `make observatory-up` on clean VPS (no prior Docker images): all containers healthy within 90 seconds.
- All four subdomains serve correctly:
  - `curl https://gateway.yourdomain.com/health` → 200
  - `curl https://demo.yourdomain.com` → 200 HTML
  - `curl https://grafana.yourdomain.com` → Grafana login page
  - `curl https://observatory.yourdomain.com/api/health` → 200 with `spec_version: "2.2"`
- TLS: `curl -v https://gateway.yourdomain.com` → no SSL error; issuer is Let's Encrypt.
- `curl localhost:9090/admin/circuit-breakers/reset` from VPS host → connection refused.

**M6.2 — Live stack verification (1 day)**

Acceptance (all via `https://` production URLs):
- Run S6 (Severe, 120s) from `https://demo.yourdomain.com`:
  - All 8 steps from §1 observable within 90 seconds.
  - Exemplar dot appears on Grafana `gateway_request_duration_seconds` p99 panel; clicking opens Tempo trace.
  - Event feed and Circuit State panel change within 1 second of each other at CB open.
  - Money trace visible: `cb.state=open`, zero upstream span, < 1ms total.
  - Reset returns `{"status":"clean"}` within 15 seconds.
- Run S3 at Moderate: 429s appear in Grafana Dashboard 3; SSE shows `rate_limited` events.

**M6.3 — Demo video recording (1 day)**

Script (§17 timestamped table):

| Time | Action |
|------|--------|
| 0:00–0:10 | Open `demo.yourdomain.com`. Show live stat counters updating. |
| 0:10–0:15 | Navigate to Observatory. Select S6, Severe, 120s. |
| 0:15–0:20 | Hit RUN. Say: "I'm going to kill one upstream instance." |
| 0:20–0:50 | Watch: green events → red `circuit OPEN` → collapsed 503 events. |
| 0:50–1:00 | Click `[View Trace →]` on `circuit_open`. Show Tempo: `cb.state=open`, zero upstream span, < 1ms. |
| 1:00–1:10 | Return to Observatory. Click exemplar dot on Grafana latency panel. Same trace opens. |
| 1:10–1:30 | Watch: `half-open probe sent` → `circuit CLOSED` → green traffic. |
| 1:30–1:40 | Click RESET ALL; confirm; show `{"status":"clean"}`. |
| 1:40–1:50 | Navigate to Observability → Traces tab. Show trace list. |
| 1:50–2:00 | Close with one sentence. Stop recording. |

Acceptance:
- Duration: 2:00 ± 15 seconds.
- Zero ad-lib — system narrates every step.
- Video linked from `README.md` Phase 9 section.
- Script rehearsed ≥5 times before recording.

**M6 Acceptance (project done):**
All §17 success criteria checked on production VPS. Every checkbox verified against live
traffic, not just locally. Video recorded and linked.

---

## 3. Cross-Cutting Requirements

### Responsive UI

| Viewport | Required behavior |
|---------|------------------|
| ≥1280px | Full three-column Observatory; horizontal pipeline; all panels visible |
| 1024–1279px | Observatory: right metric column stacks below event feed; pipeline: smaller nodes |
| <1024px | Single column; metrics behind toggle; "Best viewed on desktop" banner |
| ≈375px | Landing and About fully usable; no horizontal scroll |

### Security (summary table)

| Requirement | Mechanism | Verified in |
|-------------|-----------|------------|
| DEMO_TOKEN on mutating endpoints | `hmac.Equal`; 401 on fail | M5.2 |
| ADMIN_TOKEN on admin endpoint | `hmac.Equal`; 401 on fail; internal Docker only | M1.4 |
| Metrics proxy 60/min limit (§8.7) | Separate token bucket on `/api/metrics/*` | M2.6, M5.2 |
| Global 100/min limit (§11.2) | Separate token bucket on all endpoints | M2.6, M5.2 |
| SSE sanitization | Strip JWT-pattern and secret-match values | M2.5, M5.2 |
| Admin endpoint not reachable from host | Not in `ports:` + Caddy bypass | M1.4, M6.1 |
| Grafana embedding | `allow_embedding = true`; `content_security_policy = false` (demo VPS only) | M1.5 |
| Redis + Prometheus not exposed | Internal Docker network only | Existing |
| Pinned image tags | In Compose overlay; no `:latest` | M1.5 |

### Definition of Done (project-level)

Done when:
1. Every §17 checkbox is verified on the **production VPS** (not locally).
2. The 2-minute demo video is recorded and linked from `README.md`.
3. All make targets pass: `make test`, `make test-race`, `make lint`, `make coverage` (≥70%), `make web-lint`.

---

## 4. Risk Register

### R1 — Exemplar pipeline end-to-end failure (High probability, High impact)

**Risk:** Four coordinated pieces are required (§6.2). Any single missing piece silently
breaks the chain — no dot appears, no error is thrown. The four pieces are:
1. Gateway `ObserveWithExemplar()` call
2. Prometheus `exemplar_storage: true` + OpenMetrics scrape format
3. Grafana `exemplarTraceIdDestinations` pointing to correct Tempo datasource UID
4. Grafana panel with exemplars enabled

**Mitigation:**
- M1.5 is not done until the dot appears and clicking it opens the correct Tempo trace.
  This is the M1 exit gate — not a check performed later.
- Diagnostic checklist to isolate which piece is missing:
  - `curl -H "Accept: application/openmetrics-text" gateway:8080/metrics | grep "# {traceID"` — Step 1 check.
  - `curl "prometheus:9090/api/v1/query_exemplars?query=gateway_request_duration_seconds&start=...&end=..."` — Step 2 check.
  - Inspect `monitoring/grafana/provisioning/datasources/prometheus.yaml` for `exemplarTraceIdDestinations.datasourceUid` = `tempo` — Step 3 check.
  - Grafana panel editor → Field → Exemplars toggle — Step 4 check.
- Do not proceed to M2 if M1.5 exit criterion is not met.

### R2 — Docker log stream multiplexing (Medium probability, High impact)

**Risk:** `ContainerLogs` returns a multiplexed stream with an 8-byte header per chunk
(stream type + 3 zero bytes + 4-byte big-endian length). Parsing raw bytes as JSON
produces garbage. The Observatory emits zero SSE events with no error — silent failure.

**Mitigation:**
- Always use `stdcopy.StdCopy(lineWriter, io.Discard, logs)` from
  `github.com/docker/docker/pkg/stdcopy` before line parsing. This is documented as
  mandatory in spec §8.4 and is the primary defense.
- `cmd/observatory/events_test.go` must construct a `bytes.Buffer` with Docker-format
  8-byte headers prepended to real JSON log lines and assert correct output from
  `parseEvents()`. This test must pass before M2.5 is marked complete.
- Verify gateway log contract: `docker logs gateway 2>/dev/null | python3 -m json.tool > /dev/null`
  must succeed on every line (zero parse errors).

### R3 — Grafana cross-subdomain iframe embedding (Medium probability, Medium impact)

**Risk:** Browsers block iframes from `demo.yourdomain.com` embedding `grafana.yourdomain.com`
via X-Frame-Options or CSP, even with `allow_embedding = true` and
`content_security_policy = false`, in newer Grafana versions or browser security updates.

**Mitigation:**
- B2 pre-condition in M4.5: verify in Chrome and Firefox on the real VPS before building
  the Traces tab. If blocked, implement the fallback "Open in Grafana →" button
  immediately and document the decision. The rest of the Observatory page is unaffected.
- If `content_security_policy = false` is insufficient in future, set
  `content_security_policy_template` to include `frame-ancestors https://demo.yourdomain.com`
  explicitly instead of disabling CSP entirely.

### R4 — k6 container Docker network isolation (Medium probability, High impact)

**Risk:** k6 runs as a container started via Docker SDK. If the container is not on the
same Compose network as the gateway, `http://gateway:8080` is unreachable. k6 may exit
with a connection error or — worse — run silently while Prometheus shows flat RPS.
The scenario appears to execute but sends zero load.

**Mitigation:**
- In `runner.go`: set `HostConfig.NetworkMode = <COMPOSE_PROJECT_NAME>_default`.
  Read `COMPOSE_PROJECT_NAME` from Observatory environment (default: `irongate`).
  Document in `docker-compose.observatory.yml` comments.
- Every k6 script includes a `setup()` function: `http.get("http://gateway:8080/health")`
  with `check(res, {"gateway reachable": (r) => r.status === 200})`. Fails fast on
  network isolation with an obvious error.
- M2.3 acceptance criterion explicitly checks `gateway_requests_total` increases during
  k6 run — this is the network reachability verification gate.

### R5 — Toxiproxy Redis address adoption (Low probability, Medium impact)

**Risk:** For S8, the gateway must use `toxiproxy:6380` instead of `redis:6379`. If the
gateway has already pooled connections to `redis:6379` before the overlay is applied,
Toxiproxy does nothing. S8 silently fails — rate limiting is not impaired, `redis_unavailable`
events never fire, acceptance criteria are not met.

**Mitigation:**
- `configs/gateway.yaml` resolves Redis via `${REDIS_ADDR:-redis:6379}` and
  `docker-compose.observatory.yml` sets `REDIS_ADDR=toxiproxy:6380` on the gateway
  service. Docker Compose applies the env var override and restarts the gateway when the
  overlay is brought up with `docker-compose up`. Document in Compose file: "Gateway uses
  Toxiproxy for Redis in observatory mode. Do not change REDIS_ADDR without retesting S8."
- M3.3 S8 acceptance criterion explicitly verifies `gateway_rate_limit_rejections_total`
  drops to zero during Redis blackout and SSE shows `redis_unavailable` events. If this
  fails, the Toxiproxy address adoption is broken.

---

## 5. Suggested Parallel Tracks

### Dependency graph

```
M1 ──────────────────────────────────────────────────────────────► M5 ──► M6
       └──► M2 ──────────────────────────────────────────────────┘
                  ├──► M3 (scenarios) ─────────────────────────────┤
                  └──► M4 (frontend)  ─────────────────────────────┘

M4 can start as soon as M2.2 (scenario API) is stable.
M3 and M4 are fully independent of each other.
```

### Parallel execution table

| Track | Milestones | Parallel With | Notes |
|-------|-----------|--------------|-------|
| A | M1 | Nothing | First. Gate for M2. |
| B | M2 | Nothing until M1 exits | Sequential after M1. |
| C | M3 | M4 | Starts after M2 fully done. |
| D | M4 | M3 | Can start after M2.2 stable; does not require M3. |
| E | M5 | Nothing | Requires both M3 and M4. |
| F | M6 | Nothing | Requires M5. |

### Recommended single-engineer schedule (with AI implementing)

| Week | Primary Focus | Milestone Progress |
|------|--------------|-------------------|
| 1 | M1.1, M1.2, M1.3 | OTel SDK wired; outer + inner chain instrumented; traces in Tempo |
| 2 | M1.4, M1.5 | Admin endpoint; `gateway_circuit_state`; exemplars verified end-to-end |
| 3 | M2.1–M2.4 | Observatory skeleton; JWT startup via gateway login route; k6 runner; chaos |
| 4 | M2.5, M2.6; M3.1 | Docker SDK SSE feed + unit test; reset; Toxiproxy; S2 + S3 |
| 5 | M3.2–M3.4; M4.1–M4.2 | S4, S5, S7, S8, S9; frontend scaffold + landing |
| 6 | M4.3–M4.5 | About page; Observatory page (controls + event feed + panels) |
| 7 | M4.6–M4.7; M5.1 | Observability page (verify B2 first); Dashboards 2–4 |
| 8 | M5.2, M5.3; M6.1, M6.2 | Security hardening; VPS deploy; live verification |
| 9 | M6.3 | Demo video; README; PROGRESS.md; done |

### Hard sequential constraints

- M1 → M2: Observatory needs the admin endpoint and `gateway_circuit_state` from M1.
- M2 → M5: M5 verifies the full system; cannot harden what is incomplete.
- M5 → M6: Deploy only a hardened, fully verified system.
- M2.5 (SSE) → M4.5 (event feed): M4.5 acceptance requires real events. During local
  frontend dev before M2.5 is ready, build M4.5 against a mock SSE server (a trivial
  `net/http` handler emitting fixture events on a timer). Replace with the real endpoint
  for M4.5 acceptance testing.

---

*Plan version 1.2 — aligned to `docs/phase9-planning/PHASE9_CHAOS_OBSERVATORY_SPEC_v2.2.md`*
*Canonical path: `docs/phase9-planning/PHASE9_IMPLEMENTATION_PLAN_v1.2.md`*
*All Appendix B decisions locked and reflected in milestones*
*No gateway features added beyond spec scope*
*Supersedes v1.1*
