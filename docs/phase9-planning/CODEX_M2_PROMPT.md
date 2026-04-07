# Codex Implementation Prompt — Phase 9 Milestone 2 (M2)

> Generated from plan-eng-review session on Apr 07, 2026.
> This file is the verified, implementation-ready prompt to hand to Codex for M2.
> Copy everything below the horizontal rule.

---

You are implementing Phase 9 Milestone 2 (M2) of IronGate: the Observatory backend core.
Phases 1 through 8 and Phase 9 M1 are already implemented on the M2 branch base.
Your job is M2 only.

## STEP 1 — READ THESE FILES FIRST

Read all of these before writing code:

1. `docs/phase9-planning/PHASE9_CHAOS_OBSERVATORY_SPEC_v2.2.md`
   Required sections: 7.1, 7.2 S1 and S6, 8.1 through 8.9, 11.1 through 11.4, 13, 14
2. `docs/phase9-planning/PHASE9_IMPLEMENTATION_PLAN_v1.2.md`
   Required sections: M2.1 through M2.6, M2 acceptance, R2, R4, R5
3. `docs/phase9-planning/DECISIONS_LOCK.md`
   All decisions are locked. Do not invent alternate auth, scenario, or reset contracts.
4. `CLAUDE.md`
   All engineering rules still apply.
5. `docker-compose.yml`
6. `docker-compose.observatory.yml`
7. `Makefile`
8. `Dockerfile`
9. `configs/gateway.yaml`
10. `cmd/gateway/main.go`
11. `internal/middleware/auth.go`
12. `internal/middleware/ratelimit.go`
13. `internal/middleware/metrics.go`
14. `internal/transport/retry.go`
15. `internal/transport/resilient.go`
16. `internal/transport/circuitbreaker/breaker.go`
17. `internal/transport/circuitbreaker/registry.go`
18. `internal/response/response.go`
19. `internal/ratelimit/store.go`
20. `services/common/chaos.go`
21. `services/user-service/main.go`

## STEP 2 — CODEBASE FACTS YOU MUST HONOR

- `cmd/observatory/` does not exist yet.
- `scenarios/` does not exist yet.
- `docker-compose.observatory.yml` currently contains only Tempo, OTel Collector, and gateway/Grafana M1 wiring.
- `Makefile` currently starts the M1 overlay only. It does not pre-pull k6 and does not run an Observatory service.
- `Dockerfile` currently builds only `cmd/gateway`.
- The gateway already has:
  - `POST /admin/circuit-breakers/reset` on `gateway:9090`
  - `gateway_circuit_state{service}`
  - OpenTelemetry traces
  - Prometheus exemplars
- The gateway does **not** yet emit structured per-request Observatory event logs.
- Services already expose:
  - `POST /chaos/latency`
  - `POST /chaos/errors`
  - `POST /chaos/down`
  - `POST /chaos/reset`
  - `GET /health`
- `POST /api/users/login` on the gateway already returns a JWT with an empty body.
- Redis already uses `${REDIS_ADDR}` in `configs/gateway.yaml`.
- Existing response helper [`internal/response/response.go`](/Users/hiteshsadhwani/Desktop/Personal_Project/IronGate/internal/response/response.go) is available, but the Observatory public API must follow the Phase 9 spec response shape exactly.

## STEP 3 — PLAN-ENG-REVIEW FINDINGS

These are not optional polish items. Build the M2 implementation to close them.

### Finding 1 — Critical: M2 SSE is impossible unless the gateway emits structured events

Spec §8.4 says Observatory reads gateway stdout through Docker `ContainerLogs`, demultiplexes
it, and parses one JSON event per line. The current gateway only logs startup/shutdown/errors.
Without additive event emission from the gateway, `GET /api/events` will never produce
`request_success`, `circuit_open`, `circuit_closed`, `retry_attempt`, or `rate_limited`.

**Required correction:**
- M2 must include a **small gateway event-emission slice** in addition to `cmd/observatory`.
- Keep it additive only. Do not change request-routing, auth, retry, or breaker behavior.
- Emit JSON log lines with top-level keys:
  - `time`
  - `level`
  - `msg`
  - `type`
  - `attrs`
- Observatory must ignore ordinary gateway log lines and parse only lines containing `type`.

### Finding 2 — Major: Observatory needs a real lifecycle/state machine, not loose globals

M2 has one-concurrent-scenario enforcement, `/run`, `/stop`, `/status`, JWT refresh,
Docker log streaming, reset orchestration, and SSE fan-out. Ad-hoc globals will race.

**Required correction:**
- Build a central `app` or `server` struct in `cmd/observatory` that owns:
  - loaded scenarios
  - runner state
  - active scenario status
  - demo JWT cache
  - Docker client
  - HTTP clients
  - Redis client
  - SSE hub
  - rate limiters

### Finding 3 — Major: hard-coded container names will make M2 flaky

The M1 overlay currently passes `GATEWAY_CONTAINER_NAME=irongate-gateway-1`, but Compose
project names and container names can drift. M2 should not depend exclusively on a single
hard-coded runtime name.

**Required correction:**
- Discover the gateway container by Docker labels:
  - `com.docker.compose.project=<COMPOSE_PROJECT_NAME>`
  - `com.docker.compose.service=gateway`
- Support `GATEWAY_CONTAINER_NAME` as an override/fallback.
- Use the same pattern for k6 cleanup: label Observatory-created containers and query by label.

### Finding 4 — Major: auth and rate-limit boundaries must be explicit

M2 has two independent rate limits:
- `60/min` per IP on `GET /api/metrics/query` and `/query_range`
- `100/min` per IP on **all** Observatory endpoints

And only mutating endpoints require `Authorization: Bearer $DEMO_TOKEN`.

**Required correction:**
- Implement a global Observatory rate-limit middleware for `100/min`.
- Implement a second metrics-only limiter inside the metrics proxy handlers.
- Keep all GET endpoints public.
- Protect only mutating POST endpoints with constant-time `hmac.Equal`.

### Finding 5 — Major: reset must be idempotent and partial-failure-aware

`POST /api/reset` is not just a helper; it is part of M2 acceptance and is called
three times consecutively in the exit criterion.

**Required correction:**
- Implement the exact ordered reset procedure from spec §8.5.
- On failure return:
  `{"status":"partial","failed_step":"...","details":"..."}`
- Do not leave the app in a wedged "stopping forever" state after partial failures.

### Finding 6 — Medium: Observatory needs a real container/build contract

M2 cannot run from Docker until there is an Observatory binary, Docker build path, compose
service, and updated `make observatory-up`.

**Required correction:**
- Add an Observatory container build.
- Update `docker-compose.observatory.yml` with `observatory` and `toxiproxy`.
- Make `make observatory-up` pre-pull the pinned k6 image per B1.
- Mount `/var/run/docker.sock:/var/run/docker.sock` into the Observatory container so the
  Docker SDK can create k6 containers and tail gateway logs.

### Finding 7 — Medium: k6 bind mounts need the host project path, not a container-local path

The Docker daemon resolves bind mounts on the host filesystem. If Observatory runs inside
Docker and tries to mount `/app/scenarios/k6` from its own container filesystem, the daemon
will not find the scripts unless that exact path exists on the host.

**Required correction:**
- Pass the host project root into Observatory via env, e.g. `PROJECT_ROOT=${PWD}`.
- In code, derive the k6 bind source from `PROJECT_ROOT`, with `os.Getwd()` as a local
  dev fallback when Observatory runs outside Docker.

## STEP 4 — ARCHITECTURAL DECISIONS TO IMPLEMENT EXACTLY

### Decision 1 — Keep M2 logic in `cmd/observatory`, but allow one small helper file if needed

Primary files stay:
- `cmd/observatory/main.go`
- `api.go`
- `scenarios.go`
- `runner.go`
- `chaos.go`
- `events.go`
- `metrics.go`
- `reset.go`
- `toxiproxy.go`

If one extra `app.go` or `state.go` keeps the lifecycle sane, that is allowed.
Do not explode this into a large new internal package tree.

### Decision 2 — Add minimal structured gateway event logging in M2

Add a tiny helper under `internal/telemetry/` for emitting Observatory event logs.
Use the existing JSON logger and nested `attrs` object so Docker log parsing stays trivial.

Emit these event types in M2:
- `request_success`
- `auth_failed`
- `rate_limited`
- `redis_unavailable`
- `retry_attempt`
- `upstream_5xx`
- `circuit_open`
- `circuit_rejected`
- `circuit_half_open`
- `circuit_closed`
- `all_targets_exhausted`

Emit these from Observatory itself:
- `scenario_started`
- `scenario_stopped`
- `reset_complete`

Observatory sampling happens **after parsing**, per spec §8.4.

### Decision 3 — Observatory request handling contract

Implement an HTTP server on `:9000` with this route contract:

```text
GET  /api/health
GET  /api/scenarios
GET  /api/scenarios/:name
GET  /api/scenarios/:name/status
POST /api/scenarios/:name/run
POST /api/scenarios/:name/stop
GET  /api/events
GET  /api/metrics/query
GET  /api/metrics/query_range
POST /api/reset
```

Rules:
- All GETs are public.
- All POSTs require `Authorization: Bearer $DEMO_TOKEN`.
- `hmac.Equal` for token comparison.
- Response errors follow spec shape:
  `{"error":"human-readable message","code":<status>}`

### Decision 4 — Scenario state machine

Statuses are:
- `idle`
- `running`
- `stopping`
- `error`

Rules:
- One concurrent scenario only.
- `/run` while another scenario is active returns `409`.
- `/stop` transitions to `stopping`, cancels chaos orchestration, stops the k6 container,
  and returns to `idle`.
- Runner completion must clear container state even on failure.
- On failed start, status becomes `error` with a logged reason, then returns to `idle`
  after cleanup.

### Decision 5 — Gateway container discovery

Read:
- `COMPOSE_PROJECT_NAME` env var, default `irongate`
- optional `GATEWAY_CONTAINER_NAME`

Lookup order:
1. `GATEWAY_CONTAINER_NAME` if explicitly set and resolvable
2. Docker label lookup by compose project/service

Never hard-code `irongate-gateway-1` in code.

### Decision 6 — Event hub contract

Implement an in-memory SSE hub with:
- 5-minute event buffer
- per-client channel size `256`
- drop-oldest behavior when a client buffer is full
- non-blocking broadcast
- periodic pruning of events older than 5 minutes

`GET /api/events` must:
- return `Content-Type: text/event-stream`
- send `data: <json>\n\n`
- stay connected for idle periods

### Decision 7 — Query allowlist is prefix-only after trimming whitespace

Use the allowlist from spec §8.7 exactly. Reject everything else with `403`.
Apply the same query validation to `/query` and `/query_range`.

### Decision 8 — Use HTTP to service chaos endpoints, not Docker stop/start

M2 scenario chaos actions use the existing service chaos API:
- `service_down` => `POST /chaos/down`
- reset and service recovery => `POST /chaos/reset`

Do not stop service containers to simulate M2 chaos.

### Decision 9 — Docker-created k6 containers must be labeled

Add container labels such as:
- `com.irongate.observatory.managed=true`
- `com.irongate.observatory.scenario=<name>`

Reset and cleanup should use labels, not only a remembered container ID.

## STEP 5 — FILE-BY-FILE IMPLEMENTATION

### NEW: `cmd/observatory/main.go`

Responsibilities:
- JSON logger to stdout
- load env:
- `DEMO_TOKEN` required
- `ADMIN_TOKEN` required
- `DEMO_JWT` optional
- `COMPOSE_PROJECT_NAME` default `irongate`
- `GATEWAY_CONTAINER_NAME` optional
  - `PROJECT_ROOT` optional, but set in Compose for Docker-run Observatory
- init Docker client via env
- init Redis client pointed at `toxiproxy:6380` through env/compose
- load scenarios at startup
- ensure Toxiproxy proxy exists at startup
- bootstrap demo JWT:
  - if `DEMO_JWT` set, use it and log `Using static DEMO_JWT`
  - else call `POST http://gateway:8080/api/users/login` with empty body
  - exit non-zero within 10 seconds if neither path succeeds
- start a 23h JWT refresh ticker
- start the gateway log tailer / SSE hub
- serve HTTP on `:9000`
- graceful shutdown of HTTP server, Docker log tailer, JWT refresher

Health response:

```json
{
  "status": "ok",
  "spec_version": "2.2",
  "jwt_valid": true,
  "toxiproxy_ready": true
}
```

### NEW: `cmd/observatory/api.go`

Implement route wiring and thin handlers only.
The handlers should delegate to app methods instead of owning shared state.

Mutating handlers:
- `POST /api/scenarios/:name/run`
- `POST /api/scenarios/:name/stop`
- `POST /api/reset`

Return codes:
- `404` for unknown scenario
- `409` for concurrent run conflict
- `401` for auth failure

### NEW: `cmd/observatory/scenarios.go`

Define the YAML schema from spec §7.1:

```yaml
name:
display_name:
description:
what_you_learn:
what_to_watch:
category:
duration_options:
intensity_options:
chaos_sequence:
expected_signals:
k6_script:
reset_actions:
```

Implement:
- YAML loader from `scenarios/*.yaml`
- validation
- clamp/enforce caps:
  - `rps <= 500`
  - `duration <= 300`
- invalid scenario files are skipped with a startup warning, not a process crash if other
  scenarios are valid

M2 must load exactly two scenarios:
- `happy-path`
- `circuit-breaker-recovery`

### NEW: `cmd/observatory/runner.go`

Implement the k6 runner using Docker SDK.

Requirements:
- image: `grafana/k6:0.51.0`
- inspect image locally first; pull if missing
- `HostConfig.NetworkMode = <COMPOSE_PROJECT_NAME>_default`
- env passed to k6 container:
  - `RPS`
  - `DURATION`
  - `TARGET_URL=http://gateway:8080`
  - `JWT=<demo_jwt>`
- bind mount:
  - `<PROJECT_ROOT>/scenarios/k6:/scripts:ro`
- command:
  - `run /scripts/<scenario>.js`
- stream container stdout to Observatory logs
- track active container ID
- label Observatory-managed k6 containers
- enforce one concurrent scenario

### NEW: `cmd/observatory/chaos.go`

Implement chaos sequence execution based on `chaos_sequence`.

For M2 support at least:
- `service_down`

Implementation:
- start chaos goroutine alongside k6 start
- sleep until each `at_seconds`
- call the service chaos endpoint over HTTP
- cancel goroutine on `/stop`

Use service hostnames directly:
- `user-service-1`
- `user-service-2`
- `order-service-1`
- `order-service-2`
- `payment-service-1`

### NEW: `cmd/observatory/events.go`

Implement:
- Docker `ContainerLogs(Follow: true, ShowStdout: true, ShowStderr: false)`
- `stdcopy.StdCopy()` demux before parsing
- parse only JSON lines containing `type`
- map slog JSON to the SSE event schema:
  - `time` -> `ts`
  - `level`
  - `type`
  - `msg` -> `message`
  - `attrs`
- sanitize `attrs` values before buffering/emitting:
  - JWT-like strings matching `^eyJ...`
  - exact matches for `$ADMIN_TOKEN`
  - exact matches for `$DEMO_TOKEN`
- apply sampling by event type per spec §8.4
- reconnect on EOF with backoff:
  - `1s -> 2s -> 5s -> 30s cap`
- SSE fan-out through the in-memory hub

**Required test:** `TestDockerStreamParse`
- build a `bytes.Buffer` with Docker 8-byte headers
- feed real JSON log lines through `stdcopy.StdCopy`
- assert parsed events are correct

Also add tests for:
- token sanitization
- drop-oldest client buffering behavior

### NEW: `cmd/observatory/metrics.go`

Implement a Prometheus proxy to `http://prometheus:9090`.

Endpoints:
- `GET /api/metrics/query`
- `GET /api/metrics/query_range`

Rules:
- validate allowlisted query prefixes from spec §8.7 exactly
- 10 second upstream timeout
- metrics-specific limiter: `60/min` per IP
- global limiter still applies separately
- reject non-allowlisted query with `403`

### NEW: `cmd/observatory/reset.go`

Implement the exact reset order from spec §8.5:

1. Stop all running k6 containers
2. Call `/chaos/reset` on all service instances in parallel
3. `POST http://gateway:9090/admin/circuit-breakers/reset`
4. Remove all Toxiproxy toxics on proxy `redis`
5. Flush Redis keys matching `rate_limit:*` in batches of 100
6. Wait 5 seconds
7. Check `/health` on all 5 services in parallel; retry for up to 10 seconds each
8. Return clean/partial response

Total timeout: `30s`.

### NEW: `cmd/observatory/toxiproxy.go`

Implement a tiny HTTP client for:
- idempotent `PUT /proxies/redis` on startup
- list toxics
- delete toxics during reset

Use:

```json
{
  "name": "redis",
  "listen": "0.0.0.0:6380",
  "upstream": "redis:6379",
  "enabled": true
}
```

### NEW: `internal/telemetry/events.go`

Add a minimal helper for structured gateway event logging.

Suggested shape:

```go
func LogGatewayEvent(logger *slog.Logger, level slog.Level, typ, message string, attrs map[string]any)
```

Rules:
- use nested `attrs`
- do not leak raw JWTs, `DEMO_TOKEN`, or `ADMIN_TOKEN`
- keep it additive only

### MODIFY: gateway files to emit structured Observatory events

Add only the event emission needed for M2. Do not change core gateway behavior.

At minimum:
- [`internal/middleware/auth.go`](/Users/hiteshsadhwani/Desktop/Personal_Project/IronGate/internal/middleware/auth.go)
  - emit `auth_failed`
- [`internal/middleware/ratelimit.go`](/Users/hiteshsadhwani/Desktop/Personal_Project/IronGate/internal/middleware/ratelimit.go)
  - emit `rate_limited`
  - emit `redis_unavailable` when failing open because Redis is unavailable
- [`internal/middleware/metrics.go`](/Users/hiteshsadhwani/Desktop/Personal_Project/IronGate/internal/middleware/metrics.go)
  - emit `request_success` for non-5xx terminal responses
- [`internal/transport/retry.go`](/Users/hiteshsadhwani/Desktop/Personal_Project/IronGate/internal/transport/retry.go)
  - emit `retry_attempt`
  - emit `upstream_5xx`
- [`internal/transport/resilient.go`](/Users/hiteshsadhwani/Desktop/Personal_Project/IronGate/internal/transport/resilient.go)
  - emit `circuit_rejected`
  - emit `all_targets_exhausted`
- [`internal/transport/circuitbreaker/breaker.go`](/Users/hiteshsadhwani/Desktop/Personal_Project/IronGate/internal/transport/circuitbreaker/breaker.go)
  or [`internal/transport/circuitbreaker/registry.go`](/Users/hiteshsadhwani/Desktop/Personal_Project/IronGate/internal/transport/circuitbreaker/registry.go)
  - emit `circuit_open`
  - emit `circuit_half_open`
  - emit `circuit_closed`

Each event should include `trace_id` when available.

### NEW: `scenarios/happy-path.yaml`

Create the M2 S1 definition from spec §7.2:
- category `baseline`
- no chaos
- durations `30, 60, 120`
- intensities in the healthy baseline range
- `k6_script: scenarios/k6/happy-path.js`

### NEW: `scenarios/circuit-breaker-recovery.yaml`

Implement the exact schema example from spec §7.1 / S6.
This one is locked:
- name `circuit-breaker-recovery`
- intensity options `20 / 100 / 300`
- duration options `60 / 120 / 300`
- `service_down` on `user-service-2` at `10s`
- `k6_script: scenarios/k6/circuit-breaker-recovery.js`

### NEW: `scenarios/k6/lib/common.js`

Shared helpers:
- base URL from env
- auth header from env JWT
- helper request wrappers

### NEW: `scenarios/k6/happy-path.js`

Requirements:
- `setup()` calls `GET http://gateway:8080/health`
- constant-arrival-rate executor
- reads RPS, duration, base URL, JWT from env
- sends steady healthy traffic to authenticated gateway routes

### NEW: `scenarios/k6/circuit-breaker-recovery.js`

Requirements:
- same setup behavior
- constant-arrival-rate executor
- focus traffic on `/api/users`
- use JWT from env

### MODIFY: `docker-compose.observatory.yml`

Add:
- `observatory` service on `:9000`
- `toxiproxy` service using pinned image `ghcr.io/shopify/toxiproxy:2.9.0`
- gateway env override:
  - `REDIS_ADDR=toxiproxy:6380`
- Observatory env:
  - `DEMO_TOKEN`
  - `ADMIN_TOKEN`
  - optional `DEMO_JWT`
  - `COMPOSE_PROJECT_NAME`
  - optional `GATEWAY_CONTAINER_NAME`
  - `PROJECT_ROOT=${PWD}`
- expose Observatory locally:
  - `127.0.0.1:9000:9000`
- mount Docker socket:
  - `/var/run/docker.sock:/var/run/docker.sock`

Do **not** expose:
- gateway admin `:9090`
- toxiproxy admin `:8474`
- toxiproxy redis `:6380`

### MODIFY: `Makefile`

Update:
- `build` to compile both `bin/gateway` and `bin/observatory`
- `observatory-up` to pre-pull `grafana/k6:0.51.0` before Compose up
- `observatory-down`
- `observatory-logs`
- `observatory-reset` that POSTs `/api/reset`

Keep `make lint`, `make test`, `make test-race`, `make build` as the main verification bar.

### ADD: Observatory Docker build path

Add either:
- `Dockerfile.observatory`

or
- a small multi-binary Docker build setup

Choose the simpler option. The important outcome is:
- Compose can build `observatory`
- image runs `cmd/observatory`
- no brittle runtime hacks

## STEP 6 — REQUIRED TESTS

Add unit coverage for:
- scenario YAML loading and invalid-file skipping
- caps/clamping
- JWT bootstrap fallback (`DEMO_JWT`)
- failed JWT bootstrap exits fast
- Docker log demux parsing (`TestDockerStreamParse`)
- SSE sanitization
- allowlist matching
- metrics `60/min` limiter
- global `100/min` limiter
- Toxiproxy idempotent proxy creation
- reset partial failure response
- scenario lifecycle state transitions

Gateway-side event emission tests should cover at least:
- `circuit_open`
- `circuit_closed`
- `rate_limited`
- `redis_unavailable`

## STEP 7 — VERIFICATION GATES

Before calling M2 done, all of these must pass:

1. `make lint`
2. `IRONGATE_TEST_REDIS_ADDR=127.0.0.1:6379 make test`
3. `IRONGATE_TEST_REDIS_ADDR=127.0.0.1:6379 make test-race`
4. `make build`
5. `go test ./cmd/observatory/... -run TestDockerStreamParse`

And then the live M2 acceptance:

```bash
make observatory-up

curl http://127.0.0.1:9000/api/health
# -> {"status":"ok","spec_version":"2.2","jwt_valid":true,"toxiproxy_ready":true}

curl http://127.0.0.1:9000/api/scenarios
# -> includes happy-path and circuit-breaker-recovery

curl -N http://127.0.0.1:9000/api/events

curl -XPOST \
  -H "Authorization: Bearer $DEMO_TOKEN" \
  http://127.0.0.1:9000/api/scenarios/circuit-breaker-recovery/run \
  -d '{"intensity":"moderate","duration":60}'

# Must observe within the run:
# - scenario_started
# - circuit_open
# - circuit_closed

curl http://127.0.0.1:9000/api/scenarios/circuit-breaker-recovery/status
# -> {"status":"idle"}

for i in 1 2 3; do
  curl -XPOST -H "Authorization: Bearer $DEMO_TOKEN" \
    http://127.0.0.1:9000/api/reset
done
# -> {"status":"clean"} all three times
```

Also verify:
- `gateway_requests_total` increases during a running scenario
- `GET /api/metrics/query?query=gateway_circuit_state` succeeds
- non-allowlisted PromQL returns `403`
- 61st metrics request in 60s returns `429`
- 101st global request in 60s returns `429`

## STEP 8 — THINGS YOU MUST NOT DO

1. Do not pretend `cmd/observatory` alone can satisfy the SSE contract. Add the minimal gateway event-emission slice.
2. Do not stop service containers to implement M2 chaos. Use `/chaos/down` and `/chaos/reset`.
3. Do not hard-code `irongate-gateway-1` in code.
4. Do not expose `gateway:9090` or Toxiproxy ports publicly.
5. Do not add a second auth contract such as `DEMO_USER` / `DEMO_PASS`.
6. Do not merge the two Observatory rate limits into one bucket.
7. Do not block the Docker log reader on slow SSE clients.
8. Do not parse raw Docker log bytes without `stdcopy.StdCopy()`.
9. Do not emit non-JSON or multi-line gateway event logs.
10. Do not change core gateway routing/auth/retry/circuit-breaker semantics while adding event logs.

Implement M2 in this order:

1. Observatory skeleton + health + JWT bootstrap
2. Scenario loader + `/api/scenarios*`
3. k6 runner + one-concurrent-run state machine
4. Chaos orchestration
5. Gateway event emission + Docker log parsing + SSE
6. Prometheus proxy
7. Reset procedure
8. Full acceptance verification
