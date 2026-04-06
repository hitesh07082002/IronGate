# IronGate — Phase 9: Chaos Observatory
### *The Unforgettable Demo Platform — Specification v2.2*

> **Status:** Approved Phase 9 planning spec
> **Author:** Hitesh Sadhwani
> **Spec Version:** 2.2 (supersedes v2.1)
> **Canonical path:** `docs/phase9-planning/PHASE9_CHAOS_OBSERVATORY_SPEC_v2.2.md`
> **Implementation lock:** `docs/phase9-planning/DECISIONS_LOCK.md`
> **Prerequisite:** Phases 1–8 complete and verified on `main`
> **Last Updated:** April 2026
>
> **Authority boundary:** Phase 8 on `main` is the shipped baseline. For live behavior use
> [`ARCHITECTURE.md`](../../ARCHITECTURE.md), [`PROGRESS.md`](../../PROGRESS.md), and
> [`deploy/README.md`](../../deploy/README.md). This document is authoritative for planned
> Phase 9 work only until that phase ships.
>
> **Scope of Phase 9 gateway changes:** No changes to routing, auth, rate-limit, retry,
> or circuit-breaker *semantics*. Additions are observability hooks only: OTel span
> instrumentation, a `gateway_circuit_state` Prometheus gauge, a `ObserveWithExemplar`
> call on `gateway_request_duration_seconds`, and an internal admin HTTP server on
> `:9090` for circuit-breaker state reset. Everything else in Phase 9 is new infrastructure
> layered on top of the existing gateway.
>
> **All Appendix B decisions (B1–B5) are closed and incorporated into this document.**
> [`DECISIONS_LOCK.md`](./DECISIONS_LOCK.md) freezes the implementation choices that must
> not drift while Phase 9 is being built. Appendix B is retained for traceability only.
> Do not re-open without a specific technical reason.

---

## Table of Contents

1. [Vision and Definition of Done](#1-vision-and-definition-of-done)
2. [North Star Principles](#2-north-star-principles)
3. [What Gets Built](#3-what-gets-built)
4. [Tracing Stack Decision](#4-tracing-stack-decision)
5. [OpenTelemetry Instrumentation](#5-opentelemetry-instrumentation)
6. [Prometheus Exemplars](#6-prometheus-exemplars)
7. [Scenario Library](#7-scenario-library)
8. [Observatory Service](#8-observatory-service)
9. [Frontend](#9-frontend)
10. [Grafana Dashboards](#10-grafana-dashboards)
11. [Security and Data Sanitization](#11-security-and-data-sanitization)
12. [Implementation Order](#12-implementation-order)
13. [Project Structure](#13-project-structure)
14. [Makefile Targets](#14-makefile-targets)
15. [Deployment](#15-deployment)
16. [VPS Sizing](#16-vps-sizing)
17. [Success Criteria](#17-success-criteria)
18. [What This Delivers in an Interview](#18-what-this-delivers-in-an-interview)
19. [Appendix A — Changes from v2.1](#appendix-a--changes-from-v21)
20. [Appendix B — Closed Decisions (traceability only)](#appendix-b--closed-decisions-traceability-only)

---

## 1. Vision and Definition of Done

Most portfolio projects end with a README and a GitHub link.

IronGate ends with an interviewer sitting in front of a live system, choosing a scenario,
watching a circuit breaker open in real time, clicking a 503 event in the feed, and
seeing the exact trace — down to the millisecond — of why that request was rejected
before it ever reached an upstream service.

That experience cannot be faked. It proves the system works because the interviewer
ran it themselves.

**Definition of done:**

A stranger opens `demo.yourdomain.com`, picks "Circuit Breaker: Open → Recovery",
sets intensity to Severe, hits Run, and in under 90 seconds observes — without any
explanation from you:

1. Green routing events flowing in the live event feed
2. `circuit OPEN on user-service-2` firing in red
3. The Circuit State timeline panel flipping to OPEN
4. 503s appearing on the error rate graph
5. A Grafana exemplar dot on the latency spike — click → Tempo trace opens
6. The trace waterfall: `cb.state=open`, zero upstream span, request rejected in < 1ms
7. Half-open probe firing, circuit closing, green traffic resuming automatically
8. One Reset button restoring clean state in under 15 seconds

No explanation needed. The system narrates itself.

---

## 2. North Star Principles

**The gateway stays the hero.** Every UI element exists to make gateway behavior
visible. Phase 9 is a lens, not a feature set.

**The demo is a product.** Scenarios are curated, capped, and rehearsed. Every preset
works every time. There are no free-form URL inputs, no unbounded RPS fields, nothing
that can break the Docker network.

**Evidence travels with the repo.** Scenario definitions, k6 scripts, dashboard JSON,
OTel config, and pinned image versions all live in git. The full platform is reproducible
from `docker-compose up` on a fresh VPS.

**Observability is first-class.** Metrics, traces, and logs share the same `request_id`,
`service` labels, and naming conventions. One ID, three views.

**Sliders control intensity, not behavior.** The user picks a scenario (what the
gateway experiences) and tunes how hard to push it (RPS, duration, chaos level).
Parameter caps are enforced server-side — the UI slider max is advisory, the server
enforces the real ceiling.

---

## 3. What Gets Built

### 3.1 New Components

| Component | Language | Purpose |
|-----------|----------|---------|
| `cmd/observatory` | Go | Demo control API: scenario runner, chaos orchestrator, SSE event feed, Prometheus query proxy, reset |
| `web/` | React + TypeScript | Four-page frontend: Landing, About, Chaos Observatory, Observability |
| `scenarios/` | YAML + k6 JS | Nine scenario definitions and k6 load scripts |
| `internal/telemetry/` | Go | OTel SDK initialization (gateway only) |
| `otel/` | YAML | OTel Collector config |

### 3.2 New Infrastructure (Docker Compose overlay)

All Phase 9 infrastructure ships in `docker-compose.observatory.yml`, a Compose override
layered on top of `docker-compose.yml`. All existing containers are unchanged.

**Pinned image versions (update in Compose overlay; tag `:latest` is not used):**

| Container | Image | Pinned Tag |
|-----------|-------|-----------|
| `tempo` | `grafana/tempo` | `2.4.1` |
| `otel-collector` | `otel/opentelemetry-collector-contrib` | `0.97.0` |
| `toxiproxy` | `ghcr.io/shopify/toxiproxy` | `2.9.0` |
| `observatory` | local build | — |
| `web` | local build | — |

> Update these tags when upgrading. Do not use `:latest` in production Compose files
> — it prevents reproducible demos and can silently break on image updates.
> Pinned tags are bumped only via an explicit PR that reruns `make observatory-up`,
> the smoke path, and the demo walkthrough or replay script.

**Internal ports (container-to-container only; none published to the host in the overlay):**

| Container | Internal Port(s) | Notes |
|-----------|-----------------|-------|
| `observatory` | 9000 | Public via Caddy at `observatory.yourdomain.com` |
| `web` | 3001 | Public via Caddy at `demo.yourdomain.com` |
| `tempo` | 3200 (HTTP query), 4317 (OTLP ingestion) | Tempo and Collector both use 4317 on different containers — no collision |
| `otel-collector` | 4317 (gRPC receiver), 4318 (HTTP receiver) | Gateway → `otel-collector:4317` → `tempo:4317` |
| `toxiproxy` | 8474 (admin API), 6380 (Redis proxy) | Gateway uses `toxiproxy:6380` in observatory mode |

### 3.3 Gateway Changes (observability and admin hooks only)

The following are the **only** changes to `cmd/gateway/` and `internal/` for Phase 9.
No routing, auth, rate-limit, retry, or circuit-breaker *semantics* change.

1. **OTel span instrumentation** — `internal/telemetry/telemetry.go` added; `Tracer`
   dependency injected into existing middleware and transport constructors. Activates only
   when `OTEL_EXPORTER_OTLP_ENDPOINT` env var is set; otherwise a no-op tracer is used
   and the gateway is identical to Phase 8.

2. **`gateway_circuit_state{target}` Prometheus gauge** — new gauge in the circuit
   breaker registry; values 0=CLOSED, 1=OPEN, 2=HALF_OPEN; updated on every state
   transition. Required by the Circuit State Timeline panel in the Observatory UI (§9.4)
   and Dashboard 2 (§10).

3. **`ObserveWithExemplar` on `gateway_request_duration_seconds`** — the existing
   histogram observation in the tracing middleware is conditionally replaced with
   `ObserveWithExemplar()` when a sampled trace span is active. This is distinct from
   OTel span creation; both must be implemented for the exemplar → trace link to work.
   See §6 for the complete four-step exemplar pipeline.

4. **Admin HTTP server on `:9090`** — second `http.Server` in `cmd/gateway/main.go`,
   bound to `0.0.0.0` within the Docker network. Handles `POST /admin/circuit-breakers/reset`
   only (see §8.6). This port is **never** listed in `docker-compose.yml` `ports:` and
   Caddy never proxies it. Reachable only as `http://gateway:9090` from within the Docker
   network.

**Constraints:**
- All existing tests pass unmodified after these additions
- No new `gateway.yaml` config fields required for Phase 1–8 functionality
- No new middleware in the request pipeline

---

## 4. Tracing Stack Decision

**Decision: Tempo as trace store, Grafana as trace UI. No Jaeger.**

| Criterion | Tempo + Grafana | Jaeger all-in-one |
|-----------|----------------|-------------------|
| Grafana-native trace UI | ✅ Built in | ❌ Separate UI |
| Exemplar → trace link from Grafana | ✅ First-class | ❌ Requires extra config |
| Metrics + traces in one tab | ✅ Yes | ❌ No |
| New containers required | 1 (Tempo) | 1 (Jaeger) |
| New public subdomains required | 0 | 1 (`jaeger.*`) |

The exemplar dot on a metrics chart linking directly to the trace in the same Grafana
tab is the single most impressive moment in the demo. That moment requires Tempo.

**Tempo datasource provisioning:**

```yaml
# monitoring/grafana/provisioning/datasources/tempo.yaml
apiVersion: 1
datasources:
  - name: Tempo
    uid: tempo
    type: tempo
    url: http://tempo:3200
    access: proxy
    basicAuth: false
    jsonData:
      serviceMap:
        datasourceUid: prometheus
      nodeGraph:
        enabled: true
      search:
        hide: false
      traceQuery:
        timeShiftEnabled: true
        spanStartTimeShift: '-1h'
        spanEndTimeShift: '1h'
      spanBar:
        type: 'Tag'
        tag: 'http.status_code'
```

> `lokiSearch` and `tracesToLogsV2` fields are intentionally omitted — Loki is not in
> this stack and including them causes Grafana provisioning warnings.

---

## 5. OpenTelemetry Instrumentation

### 5.1 Philosophy

Every middleware stage in the outer chain becomes a span. Every transport layer in the
inner chain becomes a child span. Retry attempts become child spans of the transport
span. The resulting waterfall makes the two-tier pipeline visually undeniable.

### 5.2 SDK Initialization

```go
// internal/telemetry/telemetry.go

// Init initializes the OTel TracerProvider and returns a shutdown function.
// If OTEL_EXPORTER_OTLP_ENDPOINT is not set, returns a no-op TracerProvider.
// The OTLP exporter uses a 5-second connection timeout and 10-second export
// timeout to prevent unbounded goroutines if the Collector is unreachable.
// Call shutdown(ctx) in main() after server.Shutdown() completes.
func Init(ctx context.Context, serviceName, version string) (trace.TracerProvider, func(context.Context) error)
```

- The returned `TracerProvider` is passed as a dependency into middleware and transport
  constructors. No global tracer state — keeps tests clean and injection explicit.
- Sampling via standard OTel env vars:
  - Production: `OTEL_TRACES_SAMPLER=parentbased_traceidratio`, `OTEL_TRACES_SAMPLER_ARG=0.1`
  - Demo mode (set in overlay): `OTEL_TRACES_SAMPLER=always_on`
- If the Collector is unreachable, the exporter retries with backoff; it does not block
  request handling. Log a warning on first failure; do not log on every failed export.

### 5.3 Target Trace Structure

**Happy path with one retry:**

```
irongate.request  68ms  [traceID: abc123]
  ├── irongate.middleware.tracing      0.05ms
  │     request_id=req-a1b2  http.method=GET  http.path=/api/orders  [route template]
  ├── irongate.middleware.router       0.10ms
  │     route.service=order-service  route.path=/api/orders  route.matched=true
  ├── irongate.middleware.auth         1.80ms
  │     auth.outcome=passed  auth.user_id=<8-char hash>  auth.role=user
  ├── irongate.middleware.ratelimiter  2.10ms
  │     ratelimit.outcome=allowed  ratelimit.remaining=47  ratelimit.client_key=<8-char hash>
  └── irongate.proxy                  64ms
        ├── irongate.transport.retry.attempt  18ms  [retry.attempt=1]
        │     retry.reason=upstream_5xx
        │     ├── irongate.transport.loadbalancer  0.05ms
        │     │     lb.strategy=round_robin  lb.selected=order-service-1:8082
        │     ├── irongate.transport.circuitbreaker  0.02ms
        │     │     cb.target=order-service-1:8082  cb.state=closed
        │     └── irongate.transport.upstream  17ms
        │           upstream.target=order-service-1:8082  upstream.status=503
        ├── irongate.transport.retry.backoff  12ms
        │     retry.backoff_ms=12  retry.attempt=1
        └── irongate.transport.retry.attempt  34ms  [retry.attempt=2]
              retry.reason=upstream_5xx
              ├── irongate.transport.loadbalancer  0.05ms
              │     lb.strategy=round_robin  lb.selected=order-service-2:8092
              ├── irongate.transport.circuitbreaker  0.02ms
              │     cb.target=order-service-2:8092  cb.state=closed
              └── irongate.transport.upstream  34ms
                    upstream.target=order-service-2:8092  upstream.status=200
```

**Request rejected by open circuit breaker — the money trace:**

```
irongate.request  0.8ms  [traceID: def456]  STATUS=ERROR
  ├── irongate.middleware.tracing      0.05ms
  │     http.path=/api/users  [route template, not /api/users/42]
  ├── irongate.middleware.router       0.10ms
  ├── irongate.middleware.auth         0.20ms
  ├── irongate.middleware.ratelimiter  0.30ms
  └── irongate.proxy                   0.10ms
        └── irongate.transport.circuitbreaker  0.05ms  STATUS=ERROR
              cb.target=user-service-2:8092
              cb.state=open
              [event: circuit_rejected]
```

Zero upstream span. Rejected in under 1ms. This trace proves the circuit breaker works
by showing what does not happen.

### 5.4 Span Attributes Contract

**All attributes must match this table exactly. Deviations are bugs.**

**Root span (set by `irongate.middleware.tracing`, propagated via context):**

| Attribute | Type | Value — authoritative rule |
|-----------|------|---------------------------|
| `request_id` | string | Gateway-generated UUID; matches `X-Request-ID` response header |
| `http.method` | string | GET, POST, PUT, DELETE, etc. |
| `http.path` | string | **Route template only** — e.g. `/api/users`, never `/api/users/42`. Use `route.Path` from the matched `RouteConfig`, not `req.URL.Path`. |
| `http.status_code` | int | Final response status code; set on span end, not span start |

**Outer chain — per middleware span:**

| Attribute | Set By | Authoritative Values |
|-----------|--------|---------------------|
| `route.service` | Router | `order-service`, `user-service`, `payment-service` |
| `route.path` | Router | `/api/orders`, `/api/users`, `/api/payments` — route template |
| `route.matched` | Router | `true`, `false` |
| `auth.outcome` | Auth | `passed`, `failed`, `skipped` |
| `auth.user_id` | Auth | First 8 hex chars of `SHA-256(jwt_sub_claim)` — never raw user ID |
| `auth.role` | Auth | JWT `role` claim; low-cardinality (e.g. `user`, `admin`) |
| `ratelimit.outcome` | RateLimiter | `allowed`, `rejected`, `fail_open` |
| `ratelimit.remaining` | RateLimiter | `int64` |
| `ratelimit.client_key` | RateLimiter | First 8 hex chars of `SHA-256(user_id_or_ip)` |

**Inner chain — per transport layer span:**

| Attribute | Set By | Authoritative Values |
|-----------|--------|---------------------|
| `lb.strategy` | LoadBalancer | `round_robin`, `weighted`, `least_conn` |
| `lb.selected` | LoadBalancer | `host:port` (internal Docker hostname) |
| `cb.target` | CircuitBreaker | `host:port` |
| `cb.state` | CircuitBreaker | `closed`, `open`, `half_open` |
| `retry.attempt` | Retry | `int`, 1-based |
| `retry.reason` | Retry | `upstream_5xx`, `connection_failure`, `timeout` |
| `retry.backoff_ms` | Retry | `int64` |
| `upstream.target` | Upstream | `host:port` |
| `upstream.status` | Upstream | HTTP status code as `int` |
| `upstream.duration_ms` | Upstream | `float64` |

### 5.5 Implementation Rules

- Use `go.opentelemetry.io/otel` SDK only. No vendor-specific SDKs.
- Exporter: OTLP gRPC to `otel-collector:4317`, configured via `OTEL_EXPORTER_OTLP_ENDPOINT`.
- Propagation: W3C TraceContext (`traceparent`/`tracestate`). Gateway injects these headers into upstream requests before forwarding.
- Span names follow `irongate.<layer>.<component>` exactly. No path or user data in names.
- High-cardinality values go in span **attributes** only — never in span **names**.
- Retry attempts share the span name `irongate.transport.retry.attempt`, differentiated by the `retry.attempt` attribute value, not by appending numbers to the name.
- The `request_id` attribute on the root span is the cross-link between structured logs (which carry `request_id`) and traces (which carry `traceID`). Observatory event feed uses this.

### 5.6 OTel Collector Configuration

```yaml
# otel/collector-config.yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
      http:
        endpoint: 0.0.0.0:4318

processors:
  memory_limiter:
    limit_mib: 256
    spike_limit_mib: 64
    check_interval: 5s
  batch:
    timeout: 1s
    send_batch_size: 1024

exporters:
  otlp/tempo:
    endpoint: tempo:4317
    tls:
      insecure: true

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [memory_limiter, batch]   # memory_limiter MUST precede batch
      exporters: [otlp/tempo]
```

---

## 6. Prometheus Exemplars

### 6.1 What Exemplars Are

An exemplar is metadata attached to a specific histogram bucket observation that links
it to a trace. In Grafana, exemplars appear as clickable dots on a metric chart.
Clicking one opens the Tempo trace that was active at that measurement.

This is the connective tissue between metrics and traces: a latency spike on a chart
becomes directly navigable to the exact trace that caused it.

### 6.2 Four-Step Pipeline (all four steps required)

Exemplars do not come from OTel spans automatically. They require explicit code in the
Prometheus histogram observation path. Any single missing step silently breaks the
chain — the dot does not appear and no error is thrown.

**Step 1 — Extract span context after the root span starts (in tracing middleware):**

```go
import (
    "go.opentelemetry.io/otel/trace"
    "github.com/prometheus/client_golang/prometheus"
)

spanCtx := trace.SpanFromContext(ctx).SpanContext()
```

**Step 2 — Observe with exemplar on request completion:**

```go
if spanCtx.IsValid() && spanCtx.IsSampled() {
    // Type assertion is safe: WithLabelValues on a HistogramVec returns an
    // Observer that also implements ExemplarObserver.
    gatewayRequestDuration.WithLabelValues(service).(prometheus.ExemplarObserver).ObserveWithExemplar(
        durationSeconds,
        prometheus.Labels{"traceID": spanCtx.TraceID().String()},
    )
} else {
    gatewayRequestDuration.WithLabelValues(service).Observe(durationSeconds)
}
```

**Step 3 — Enable exemplar storage and OpenMetrics scrape format in Prometheus:**

```yaml
# monitoring/prometheus/prometheus.yml
global:
  scrape_interval: 15s

storage:
  exemplars:
    max_exemplars: 100000

scrape_configs:
  - job_name: irongate
    static_configs:
      - targets: ['gateway:8080']
    # scrape_protocols requests OpenMetrics format, which carries exemplars.
    # Requires Prometheus ≥2.49. For older Prometheus versions, use instead:
    #   params: { format: ['openmetrics-text'] }
    scrape_protocols:
      - OpenMetricsText1.0.0
      - OpenMetricsText0.0.1
      - PrometheusText0.0.4
```

**Step 4 — Wire exemplar → trace link in Grafana Prometheus datasource:**

```yaml
# monitoring/grafana/provisioning/datasources/prometheus.yaml
apiVersion: 1
datasources:
  - name: Prometheus
    uid: prometheus
    type: prometheus
    url: http://prometheus:9090
    jsonData:
      exemplarTraceIdDestinations:
        - name: traceID
          datasourceUid: tempo    # must match uid in tempo.yaml exactly
```

**Which metric gets exemplars:** Only `gateway_request_duration_seconds`. Exemplars on
counters provide less value and add noise.

**Sampling constraint:** Exemplars emit only when `spanCtx.IsSampled()` is true.
In demo mode (`OTEL_TRACES_SAMPLER=always_on`), every request emits an exemplar.
In production mode (10% ratio), only sampled requests do. This is correct behavior.

---

## 7. Scenario Library

### 7.1 Scenario Definition Schema

```yaml
# scenarios/circuit-breaker-recovery.yaml
name: "circuit-breaker-recovery"
display_name: "Circuit Breaker: Open → Recovery"
description: "Kill one upstream replica and watch the circuit open, traffic fail over, then automatically recover."
what_you_learn: "How circuit breakers prevent cascade failures and self-heal without operator intervention."
what_to_watch: "Watch the Circuit State timeline — it flips OPEN within ~10 seconds, then probes and CLOSES automatically."
category: resilience         # resilience | auth | rate-limiting | latency
duration_options: [60, 120, 300]
intensity_options:
  mild:     { rps: 20 }
  moderate: { rps: 100 }
  severe:   { rps: 300 }
chaos_sequence:
  - at_seconds: 10
    action: service_down
    target: user-service-2
  # No recovery action during the run — the circuit breaker self-heals.
  # user-service-2 is restarted only during the reset procedure.
expected_signals:
  - panel: "Circuit State"
    signal: "Flips CLOSED → OPEN within 15s of chaos start"
  - panel: "RPS + Error Rate"
    signal: "503 spike during OPEN period; drops on recovery"
  - panel: "Upstream Picks per Target"
    signal: "user-service-2 picks drop to zero during OPEN"
  - event_feed:
      - "circuit OPEN on user-service-2"
      - "half-open probe sent to user-service-2"
      - "circuit CLOSED — recovery complete"
k6_script: "scenarios/k6/circuit-breaker-recovery.js"
reset_actions:
  - service_up: user-service-2
  - flush_circuit_breakers: true
  - drain_wait_seconds: 5
```

> **Acceptance numbers in scenarios are correct given the default scenario YAML values.**
> If `gateway.yaml` rate limits or circuit breaker thresholds are changed from defaults,
> scenario acceptance numbers (e.g. "upstream RPS ≈ 0.83/s") must be recalculated.
> Always tag scenario acceptance values as "given default YAML" in test scripts.

### 7.2 The Nine Scenarios

---

#### S1: Happy Path

**Category:** Baseline
**Intent:** Establish what healthy looks like before any chaos.
**What the audience learns:** Normal routing, load balancing across instances, zero error rate, steady latency.
**Slider:** RPS 10–200. Duration 30s / 60s / 120s. No chaos.
**Expected signals:**
- RPS panel: steady line at configured rate
- Error rate: flat zero
- Latency p99: < 10ms (gateway overhead only, echo backends)
- X-Served-By alternating between instances in event feed
- `gateway_circuit_state` = 0 for all targets

---

#### S2: Auth Wall

**Category:** Auth
**Intent:** Show the gateway rejecting bad tokens before upstreams see a single request.
**What the audience learns:** JWT auth at the gateway means a flood of bad tokens generates zero upstream traffic.
**Slider:** RPS 50–500. Failure mode: missing / expired / wrong-algorithm / invalid-signature. Duration 30s / 60s.
**Chaos:** k6 sends requests with the selected JWT failure mode. No service chaos.
**Expected signals:**
- 401 spike on error rate panel
- Upstream RPS: flat zero
- Auth failures counter climbs at configured RPS
- Latency: very low (rejection is cheap — no upstream call)
**Trace to show:** Root span + `irongate.middleware.auth` span (`auth.outcome=failed`). No proxy span. Terminated in < 2ms.

---

#### S3: Rate Limit Storm — Single Key

**Category:** Rate Limiting
**Intent:** Show the Redis sliding window rate limiter absorbing a burst from one client.
**What the audience learns:** One client is throttled at the gateway; upstreams see only allowed traffic.
**Slider:** Client RPS 50–500. Rate limit threshold 10 / 25 / 50 req/min (selects from scenario YAML). Duration 60s / 120s.
**Chaos:** k6 sends all requests authenticated as one user. No service chaos.
**Expected signals** (given 50 req/min threshold, 200 RPS client):
- Rate limit rejections counter climbs once threshold crossed
- 429 rate ≈ 200 − (50/60) ≈ 199.2 req/s after threshold
- Upstream RPS ≈ 50/60 ≈ 0.83/s (gateway absorbs excess) — **given default scenario YAML**
- X-RateLimit-Remaining drops to 0 and holds
**Variant (UI toggle):** "Many Keys" — 50 concurrent users each at their own limit. No single user gets 429; total upstream RPS ≈ 50 × 0.83 ≈ 41/s — **given default scenario YAML**.
**Trace to show:** `ratelimit.outcome=rejected`, `ratelimit.remaining=0`. No proxy span. Terminated in < 5ms.

---

#### S4: Single Replica Death — Failover

**Category:** Resilience
**Intent:** Show per-target circuit breaker isolation when one instance dies.
**What the audience learns:** Killing one replica shifts traffic to healthy replicas without affecting other targets.
**Slider:** RPS 50–300. Target: `user-service-2` / `order-service-2`. Duration 60s / 120s / 300s.
**Chaos sequence:** T+10s: `service_down`. Reset restarts it.
**Expected signals** (given default gateway.yaml CB thresholds):
- X-Served-By stops showing dead target within ~10s of chaos action
- `gateway_circuit_state{target="user-service-2:8092"}` flips to 1 (OPEN) — **given default YAML**
- Upstream picks shift 100% to healthy instance
- Client error rate: brief 503 window before CB opens; < 1% after
**Traces to show:** (1) Retry waterfall — attempt 1 hits dead target, attempt 2 hits live target. (2) Post-CB-open — `cb.state=open`, immediate routing to live target, no retry needed.

---

#### S5: Upstream 5xx Storm — Retry Absorption

**Category:** Resilience
**Intent:** Show the retry transport absorbing transient upstream failures.
**What the audience learns:** Retries with full jitter absorb low-to-moderate error rates. High error rates exhaust retries and may open the circuit.
**Slider:** Error injection rate 10% / 30% / 60% / 90%. RPS 50–200. Duration 60s / 120s.
**Chaos:** `/chaos/errors` on one upstream instance at configured rate.
**Expected signals:**
- Retry counter climbs proportionally to error rate
- At 10–30%: client error rate ≈ 0 (retries absorb failures)
- At 60–90%: retry exhaustion counter climbs; client 503s appear; circuit may open
- Upstream duration p99 increases (two upstream calls + backoff per retried request)
**Trace to show:** `attempt_1` span (503) → `retry.backoff` span → `attempt_2` span (200, different target). One root, full timing breakdown.

---

#### S6: Circuit Breaker — Open → Half-Open → Closed

**Category:** Resilience
**Intent:** The signature scenario. Full CB lifecycle, self-healing, zero operator action.
**What the audience learns:** How a circuit breaker detects failure, stops sending traffic, probes for recovery, and resumes automatically.
**Slider:** Intensity: Mild 20 RPS / Moderate 100 / Severe 300. Duration: 300s cap (recovery happens automatically, typically well before the cap).
**Chaos sequence:**
- T+10s: `service_down` on `user-service-2`
- T+auto: circuit opens as failure threshold is reached (gateway detects this, no operator action)
- T+auto+30s: gateway transitions to HALF-OPEN, sends probe request
- T+auto+35s: probe succeeds against healthy `user-service-1`; circuit closes; normal traffic resumes
- Dead service restarted only during reset, not during the run
**Expected signals — in order:**
1. Event feed: green → red `circuit OPEN` → red `503 circuit open` events (5% sampled) → yellow `half-open probe sent` → green `circuit CLOSED`
2. `gateway_circuit_state{target="user-service-2:8092"}` timeline: 0 → 1 → 2 → 0
3. Error rate graph: spike during OPEN; drops to zero on CLOSED
4. Exemplar dot on latency spike → click → Tempo trace with `cb.state=open`

**The money trace:**
```
irongate.request  < 1ms  STATUS=ERROR
  └── irongate.proxy
        └── irongate.transport.circuitbreaker
              cb.target=user-service-2:8092
              cb.state=open
              [event: circuit_rejected]
```
Zero upstream span. The circuit breaker worked.

---

#### S7: Cascading Failure — No Healthy Targets

**Category:** Resilience
**Intent:** Show the "all targets open" code path.
**What the audience learns:** When every instance is down, the gateway returns an explicit "no healthy targets" 503 rather than hanging or returning a confusing error.
**Slider:** Target service: user / order / payment. Duration 60s / 120s.
**Chaos sequence:** T+5s: kill instance 1. T+20s: kill instance 2. Reset restarts both.
**Expected signals:**
- First 20s: `gateway_circuit_state` for instance 1 = 1; traffic shifts to instance 2
- After T+20s: `gateway_circuit_state` for instance 2 = 1; SSE shows `all_targets_exhausted`; 100% 503 responses
- On reset: both `gateway_circuit_state` gauges return to 0; error rate returns to zero

---

#### S8: Redis Impaired — Fail-Open Rate Limiting

**Category:** Rate Limiting
**Intent:** Show ADR-003 (fail-open rate limiting) as observable live behavior.
**What the audience learns:** Availability is prioritized over strict enforcement during Redis degradation. This is a documented, intentional tradeoff — not a bug.
**Slider:** Impairment: latency 100ms / 500ms / packet loss 30% / full blackout. RPS 50–200. Duration 60s / 120s.
**Chaos:** Toxiproxy adds impairment to Redis via its proxy port (`:6380`). Gateway connects to `toxiproxy:6380` in observatory mode (`REDIS_ADDR=toxiproxy:6380` in Compose overlay).
**Expected signals:**
- Rate limit rejections: drop to zero (fail-open engaged)
- SSE: yellow `redis_unavailable` warning events
- X-RateLimit headers: absent on responses during fail-open
- Upstream RPS: rises above normal rate limit threshold
- On reset (toxic removed): rate limiting resumes; X-RateLimit headers return

---

#### S9: Latency Injection — Timeout and Retry Interaction

**Category:** Latency
**Intent:** Show per-route timeouts and retry behavior under slow upstreams.
**What the audience learns:** The gateway enforces per-route timeouts regardless of upstream speed. Timed-out requests trigger retry on a different target. Connection pooling bounds goroutines.
**Slider:** Injected latency 200ms / 500ms / 1000ms / 2000ms. RPS 20–100. Duration 60s / 120s.
**Chaos:** `/chaos/latency` on one upstream instance.
**Expected signals:**
- Latency p99 climbs to injected delay
- When injected delay > route timeout: 504s appear; retry fires on the fast instance
- In-flight requests panel stays bounded (< 3× normal)
- Retry-on-timeout counter climbs
**Trace to show:** `upstream.duration_ms ≈ 2000` + `deadline_exceeded` on attempt 1 → retry backoff → attempt 2 on different target at ~12ms.

---

## 8. Observatory Service

### 8.1 Responsibilities

`cmd/observatory` is a small Go HTTP service. It:
- Obtains and refreshes the demo JWT for k6 scripts (B4)
- Runs k6 load jobs inside the Docker network via Docker SDK
- Applies and removes chaos actions in sequence
- Streams gateway events via SSE using Docker SDK `ContainerLogs` (B5)
- Proxies Prometheus queries with an allowlist
- Executes the reset procedure
- Exposes `SPEC_VERSION` in the health response for deployed-demo traceability

### 8.2 API Contract

Mutating endpoints require `Authorization: Bearer $DEMO_TOKEN`.
All read-only GET endpoints are public.

```
GET  /api/health                         Observatory health + SPEC_VERSION
GET  /api/scenarios                      List all scenarios with metadata
GET  /api/scenarios/:name                Single scenario spec
POST /api/scenarios/:name/run            Start scenario — body: {"intensity","duration"}
POST /api/scenarios/:name/stop           Stop active k6 job
GET  /api/scenarios/:name/status         idle | running | stopping | error
POST /api/reset                          Full system reset (§8.5)
GET  /api/events                         SSE stream of gateway events
GET  /api/metrics/query                  Prometheus instant query (allowlisted)
GET  /api/metrics/query_range            Prometheus range query (allowlisted)
```

Health response includes version metadata:
```json
{
  "status": "ok",
  "spec_version": "2.2",
  "jwt_valid": true,
  "toxiproxy_ready": true
}
```

All error responses:
```json
{"error": "human-readable message", "code": 400}
```

### 8.3 Parameter Caps (server-enforced)

| Parameter | UI Max | Server Cap | Behavior on Exceed |
|-----------|--------|------------|-------------------|
| RPS | 500 | 500 | Clamp to 500; log warning |
| Duration | 300s | 300s | Clamp to 300s |
| Concurrent scenarios | 1 | 1 | Return 409 Conflict |

### 8.4 Event Stream (SSE)

`GET /api/events` → `Content-Type: text/event-stream`

**Implementation (B5 — locked):**

```go
// Docker ContainerLogs returns a multiplexed stream with an 8-byte header per chunk.
// Raw JSON parsing of this stream WILL FAIL silently. Always demultiplex first.
logs, _ := dockerClient.ContainerLogs(ctx, gatewayContainerID, container.LogsOptions{
    ShowStdout: true,
    ShowStderr: false,
    Follow:     true,
})
stdcopy.StdCopy(lineWriter, io.Discard, logs)  // from github.com/docker/docker/pkg/stdcopy
// Then parse lineWriter output as one-JSON-object-per-line
```

**Gateway log format contract:** One JSON object per line on stdout. No multi-line
entries, no non-JSON lines. This is pinned; any change to gateway log format requires
updating the Observatory parser in the same commit.

**Reconnection:** On EOF from `ContainerLogs` (gateway container restart), the Observatory
reconnects with exponential backoff: 1s → 2s → 5s → 30s cap.

**Backpressure:** Each SSE client has a buffered channel of size 256. On full: drop
oldest event, log a warning. Never block the `ContainerLogs` reader goroutine.

**v2 upgrade path (post-Phase 9):** Add a typed event bus in `internal/events/` where
each middleware emits structured events to a channel on state changes. Observatory
subscribes via an internal HTTP endpoint. This eliminates log parsing and provides
sub-millisecond event latency. Defer until Phase 9 is stable.

**Event schema:**

```json
{
  "ts": "2026-04-06T12:34:03.412Z",
  "level": "warn",
  "type": "circuit_open",
  "message": "circuit OPEN on user-service-2:8092",
  "attrs": {
    "target": "user-service-2:8092",
    "failure_count": 5,
    "threshold": 5,
    "request_id": "req-a1b2c3d4",
    "trace_id": "abc123def456789abcdef0123456789a"
  }
}
```

**Event types, UI colors, and sampling rates:**

| Type | UI Color | CSS Token | Sampling |
|------|----------|-----------|---------|
| `request_routed` | Grey | `ev-muted` | 1% |
| `request_success` | Green | `ev-success` | 1% |
| `auth_failed` | Yellow | `ev-warning` | 100% |
| `rate_limited` | Yellow | `ev-warning` | 100% |
| `retry_attempt` | Yellow | `ev-warning` | 100% |
| `circuit_open` | Red | `ev-error` | 100% |
| `circuit_rejected` | Red | `ev-error` | 5% |
| `upstream_5xx` | Red | `ev-error` | 5% |
| `all_targets_exhausted` | Red | `ev-error` | 100% |
| `circuit_half_open` | Yellow | `ev-warning` | 100% |
| `circuit_closed` | Green | `ev-success` | 100% |
| `redis_unavailable` | Yellow | `ev-warning` | 100% |
| `scenario_started` | Blue | `ev-system` | 100% |
| `scenario_stopped` | Blue | `ev-system` | 100% |
| `reset_complete` | Blue | `ev-system` | 100% |

Events older than 5 minutes are pruned from the in-memory SSE buffer.

### 8.5 Reset Procedure

`POST /api/reset` executes the following in order. Total timeout: 30 seconds.
The UI disables Run and shows a loading state until reset responds.

```
1. Stop all running k6 Docker containers (Docker SDK)
2. Call /chaos/reset on all service instances (HTTP, parallel)
3. POST http://gateway:9090/admin/circuit-breakers/reset (§8.6)
4. Remove all Toxiproxy toxics: DELETE /proxies/redis/toxics/:name for each
5. Flush Redis rate limit keys: SCAN 0 MATCH rate_limit:* → DEL in batches of 100
6. Wait 5 seconds (drain in-flight requests)
7. GET /health on all 5 services (parallel); retry for up to 10s each
8. Return {"status": "clean", "services_healthy": true}
```

On any step failure within 30s:
```json
{"status": "partial", "failed_step": "flush_redis", "details": "..."}
```
Operator may retry. UI re-enables Run only on `{"status": "clean"}`.

### 8.6 Circuit Breaker Flush Mechanism

**Decision: internal admin endpoint (B3). Not a container restart.**

```
POST /admin/circuit-breakers/reset
Authorization: Bearer $ADMIN_TOKEN
→ 200 {"reset": true, "targets_cleared": 4}
```

- `$ADMIN_TOKEN`: 32-byte random secret, distinct from `$DEMO_TOKEN`, set in
  `docker-compose.observatory.yml` on both gateway and Observatory containers.
- Observatory passes it as `Authorization: Bearer $ADMIN_TOKEN` when calling `gateway:9090`.
- Admin server binding: gateway runs a second `http.Server` on `:9090` bound to `0.0.0.0`
  within the Docker network. This port is **never** listed in `ports:` in any Compose file.
  Caddy never proxies it. Reachable only as `http://gateway:9090` inside Docker.
- `Registry.Reset()`: transitions all breakers to CLOSED atomically; sets all
  `gateway_circuit_state` gauges to 0.

### 8.7 Prometheus Query Proxy

Proxies to `prometheus:9090`. Allowlisted prefix match only; 10s timeout; separate
rate limits (see §11.1).

**Allowlisted query prefixes:**

```go
var allowedQueryPrefixes = []string{
    "gateway_requests_total",
    "gateway_request_duration_seconds",
    "gateway_request_failures_total",
    "gateway_rate_limit_rejections_total",
    "gateway_retries_total",
    "gateway_retry_delay_seconds",
    "gateway_circuit_opens_total",
    "gateway_open_circuits",
    "gateway_circuit_state",       // required by Circuit State Timeline panel
    "gateway_in_flight_requests",
    "gateway_upstream_duration_seconds",
    "rate(",
    "histogram_quantile(",
    "increase(",
    "sum(",
    "avg(",
}
```

Any query not matching an allowed prefix → `403 Forbidden`.

### 8.8 k6 Runner

```go
// cmd/observatory/runner.go — required behaviors
func (r *Runner) Start(ctx context.Context, scenario *Scenario, params RunParams) error {
    // 1. Pull grafana/k6:<pinned-tag> if not present locally (B1)
    // 2. Create container:
    //    - NetworkMode: <COMPOSE_PROJECT_NAME>_default  (R4 mitigation)
    //    - Env: RPS, DURATION, TARGET_URL=http://gateway:8080, JWT=<demo_jwt>
    //    - Binds: ./scenarios/k6:/scripts:ro
    //    - Cmd: ["run", "/scripts/<scenario>.js"]
    // 3. Start container; stream stdout/stderr to Observatory structured log
    // 4. Store container ID; enforce one concurrent scenario (409 if running)
}
```

Every k6 script:
- Reads all parameters from env vars (no hard-coded values)
- Includes a `setup()` function that calls `http.get("http://gateway:8080/health")` and
  asserts `status === 200` before starting load
- Uses `executor: 'constant-arrival-rate'` for predictable RPS

**Demo JWT (B4 — locked):** Observatory calls `POST http://gateway:8080/api/users/login`
at startup with an empty body. It caches the JWT and refreshes every 23 hours. Falls
back to `DEMO_JWT` env var if set (local dev). If neither path succeeds, Observatory
exits with a non-zero code.

> **Implementation note:** This intentionally uses the gateway-facing login contract that
> already exists in the repo, not a direct call to a specific `user-service-*` container.
> The underlying service route remains `POST /users/login`, but Phase 9 does not add a
> separate `DEMO_USER` / `DEMO_PASS` credential contract just for Observatory bootstrap.

### 8.9 Toxiproxy Integration

Observatory creates the Redis proxy idempotently at startup:

```
PUT http://toxiproxy:8474/proxies/redis
{
  "name": "redis",
  "listen": "0.0.0.0:6380",
  "upstream": "redis:6379",
  "enabled": true
}
```

If the proxy already exists (Observatory restart), PUT succeeds without error.

For Scenario 8, Observatory adds/removes toxics:
```
POST /proxies/redis/toxics         # add impairment
DELETE /proxies/redis/toxics/:name  # remove on reset
```

Gateway remains YAML-first in observatory mode. `configs/gateway.yaml` should resolve
Redis as `address: "${REDIS_ADDR:-redis:6379}"`, and `docker-compose.observatory.yml`
sets `REDIS_ADDR=toxiproxy:6380`. The base `docker-compose.yml` keeps the default
`redis:6379` path.

---

## 9. Frontend

### 9.1 Technology Stack

| Choice | Pinned Version | Purpose |
|--------|---------------|---------|
| React | 18.x | UI framework |
| TypeScript | 5.x (strict mode) | Type safety |
| Vite | 5.x | Dev server and build |
| Tailwind CSS | 3.x | Primary styling layer |
| shadcn/ui | components copied at install time | Primitives only |
| Recharts | 2.x | Custom live metric panels |
| React Query | 5.x | Server state management |
| EventSource API | native | SSE — no library needed |
| `tailwind-merge` | latest | Merge Tailwind classes conflict-free |
| `class-variance-authority` | latest | Variant definitions (used by shadcn internals) |
| `clsx` | latest | Conditional class composition in `cn()` |
| nginx | alpine | Static file serving in Docker |

> Pin `package.json` versions to exact semver (e.g. `"react": "18.3.1"`) not ranges
> to ensure reproducible builds. Update deliberately, not on every `npm install`.

**shadcn/ui primitives used (complete canonical list):**

Button, Dialog, Tabs, Tooltip, Select, Badge, Sheet (drawer), AlertDialog, Separator

Components are copied into `web/src/components/ui/` and owned by this repo.
Changes are made directly in the copied files — not imported from npm.

**Rules:**
- No MUI, Chakra, or other full UI kits alongside shadcn
- No stock admin/dashboard templates — all page layouts are custom
- `cn()` lives in `web/src/lib/utils.ts` using `tailwind-merge` + `clsx`

**IronGate design tokens:**

```javascript
// tailwind.config.js — extend defaultTheme, do not replace
theme: {
  extend: {
    colors: {
      // Pipeline diagram palette
      'ig-gateway':  '#3B82F6',  // blue-500   — gateway components
      'ig-redis':    '#F97316',  // orange-500 — Redis
      'ig-observe':  '#8B5CF6',  // violet-500 — Prometheus / Grafana / Tempo
      'ig-service':  '#10B981',  // emerald-500 — upstream services
      // Event feed
      'ev-success':  '#10B981',  // emerald-500
      'ev-warning':  '#F59E0B',  // amber-500
      'ev-error':    '#EF4444',  // red-500
      'ev-system':   '#3B82F6',  // blue-500
      'ev-muted':    '#6B7280',  // gray-500
      // Surfaces
      'ig-bg':       '#0A0A0B',
      'ig-surface':  '#111113',
      'ig-border':   '#1F1F23',
    },
    fontFamily: {
      mono: ['JetBrains Mono', 'Fira Code', 'Consolas', 'monospace'],
    },
  },
},
```

**Responsive breakpoints:**

| Width | Layout |
|-------|--------|
| ≥1280px | Full three-column Observatory; horizontal pipeline diagram |
| 1024–1279px | Observatory: right metric column stacks below event feed; pipeline: smaller nodes |
| <1024px | Single column; metric panels behind "Show Metrics" toggle; "Best viewed on desktop" banner on Observatory and Observability pages |
| ≈375px | Landing and About usable; stat counters stack; no horizontal scroll; CTA visible |

### 9.2 Page 1: Landing

Full-viewport, `bg-ig-bg`. Animated pipeline SVG centred, stat strip below, one CTA.

**Pipeline animation** (Prometheus-driven, 2s poll):
- Dots move when `gateway_in_flight_requests > 0`; speed scales with RPS; color proportional to error rate
- Node colors per design tokens: gateway=`ig-gateway`, Redis=`ig-redis`, services=`ig-service`, observability=`ig-observe`
- At zero RPS: static with pulsing idle glow

**Stat strip** (5s poll from Observatory Prometheus proxy):
```
REQUESTS SERVED     CIRCUIT EVENTS     RATE-LIMITED
  1,247,834            42                 8,901
```
Sources: `gateway_requests_total`, `gateway_circuit_opens_total`, `gateway_rate_limit_rejections_total`. Monospace `font-mono`.

**CTA:** "Launch Observatory →" → `/chaos`. Nav: IronGate logo, About, Observatory, Observability, GitHub ↗

### 9.3 Page 2: About

Three sections with smooth-scroll anchors.

**Section 1 — The Problem:** Two-column layout with before/after diagram.

**Section 2 — Interactive Pipeline:** Full two-tier pipeline diagram (horizontal). Each
node is clickable; opens a shadcn `Sheet` (right-side drawer) with: what-it-does, why-it's-here, failure mode, ADR link. Eight nodes, eight drawers — each drawer encodes an ADR interactively.

**Section 3 — Decision Cards:** 2×4 grid. One card per ADR: decision title, one-sentence verdict, key tradeoff, "Read ADR →" GitHub link.

### 9.4 Page 3: Chaos Observatory

**Full-screen, three-column layout.**

**Left column — 280px fixed**

Scenario picker (9 cards). Selected card expands to show: "What to watch" callout, intensity buttons (Mild / Moderate / Severe), duration buttons (60s / 120s / 300s), expected signals list.

Run controls (sticky bottom): `[▶ RUN]  [■ STOP]  [↺ RESET ALL]`. RESET ALL opens shadcn `AlertDialog`. RUN disabled during reset; re-enables only on `{"status":"clean"}`.

System status (6-dot grid, 3s poll): Gateway, Redis, user-service-1, user-service-2, order-service-1, order-service-2. Green=healthy / Red=down / Yellow=timeout.

Active scenario banner replaces picker when running: name, elapsed time, chaos timeline with checkmarks.

**Centre column — flex width**

Header: "Live Events" + pulsing ● LIVE + event counter.

Filter bar: `[All] [Routing] [Auth] [Rate Limit] [Retry] [Circuit] [System]`

Request counter strip (1s Prometheus poll): ✅ Success / ❌ Error / 🔄 Retry / ⛔ Rate-Limited

Event feed (reverse chronological, newest at top):
- Color-coded by type using CSS tokens from §8.4
- `[View Trace →]` link on events with `trace_id` → opens Grafana Explore in new tab
- High-frequency same-type events collapsed: "503 circuit open [47 events] — Expand ↓"
- Auto-scrolls to top; pauses on user scroll; "↓ Jump to latest" button
- Events pruned after 5 minutes; monospace `font-mono`

**Right column — 320px fixed**

Four Recharts panels (2s Prometheus poll via Observatory proxy), each ~140px tall.

*Panel 1 — Request Rate:* `LineChart` — total RPS (blue), error RPS (red). Y: req/s. X: last 2 minutes.

*Panel 2 — Latency Percentiles:* `LineChart` — p50 (green), p95 (yellow), p99 (red). Y: ms. X: last 2 minutes.

*Panel 3 — Circuit Breaker States:* Custom timeline. One row per upstream target (4 rows). Driven by `gateway_circuit_state{target=~".+"}` range query. Values: 0=green (CLOSED), 1=red (OPEN), 2=yellow (HALF-OPEN). X: last 2 minutes. This panel is the most visually striking during S6.

*Panel 4 — Rate Limit Activity:* Stacked `BarChart` — allowed (green base) + rejected (red top). Y: req/s. X: last 2 minutes.

**Bottom bar — 80px**

Three most recent trace IDs from SSE events with `trace_id`. Each shows method, path, status, duration, and a `[→]` link to Grafana Explore for that trace ID. Shows placeholder when idle.

### 9.5 Page 4: Observability

Three tabs using shadcn `Tabs`.

**Tab 1 — Metrics:** Four Grafana panel iframes (2×2 grid) using `d-solo` URL format.
Time range picker updates all four `src` URLs simultaneously. "Open full Grafana →" link below.

**Tab 2 — Traces:** Grafana Explore embedded via iframe (requires B2 `allow_embedding`).
Pre-configured for Tempo datasource with `{service.name="irongate"}` query.
Search bar appends TraceQL filters to the Explore URL.
Scenario trace shortcuts section: Observatory stores the last 5 trace IDs per scenario
(from SSE events with `trace_id`) and surfaces them as clickable links.

**Fallback if B2 embedding is blocked:** Replace the iframe with "Open Traces in Grafana →" button. The rest of the page remains functional. Document the fallback if taken.

**Tab 3 — Logs:** SSE stream from `/api/events`, formatted one-line-per-event in `font-mono`.
Level/Type filters; client-side substring search.
ERROR lines: red background (`ev-error`). WARN: yellow (`ev-warning`).
`req=<uuid>` values are clickable → opens Grafana Explore with a TraceQL query for that `request_id`.

---

## 10. Grafana Dashboards

Four dashboards provisioned as JSON in `grafana/dashboards/`. Zero manual setup.

**Dashboard 1 — Gateway Overview (RED)** *(shipped in M1)*
- Row 1: RPS (total/success/error stacked), error rate %, in-flight requests gauge
- Row 2: Latency p50/p95/p99 line chart with exemplar dots enabled on the p99 panel
- Row 3: Requests by service (stacked area), by status class (2xx/4xx/5xx stacked)

**Dashboard 2 — Resilience** *(shipped in M5)*
- Row 1: `gateway_circuit_state` per target — state timeline using value mapping (0=Closed/green, 1=Open/red, 2=Half-Open/yellow); circuit opens total; open circuits gauge
- Row 2: Retries total by service, retry exhaustions, retry delay histogram
- Row 3: Upstream picks per target (stacked bar — shows LB distribution shift during S4/S6/S7)

**Dashboard 3 — Rate Limiting** *(shipped in M5)*
- Row 1: Allowed vs rejected RPS (stacked bar), rejection rate %
- Row 2: Redis command latency, connection pool stats
- Row 3: Top rate-limited route paths (bounded: `route.path` has at most 10 values)

**Dashboard 4 — Scenario View (screen-sharing mode)** *(shipped in M5)*
- Four large panels: RPS + Error Rate, Circuit States (all targets), Retry Activity, Rate Limit Activity
- Grafana variables: `$scenario` (freetext annotation), `$time_range` (5m / 15m / 1h)
- Font sizes and contrast optimised for screen sharing at 1080p

---

## 11. Security and Data Sanitization

### 11.1 Rate Limits — Explicit Boundary per Route

Two separate limits apply. They are not the same value and must not be confused:

| Scope | Limit | Applied To | Mechanism |
|-------|-------|------------|-----------|
| Prometheus metrics proxy (§8.7) | **60 req/min** per IP | `GET /api/metrics/query` and `/query_range` only | In-process token bucket in Observatory |
| All Observatory API endpoints | **100 req/min** per IP | Every endpoint | In-process token bucket in Observatory |

The metrics proxy limit is more restrictive because unconstrained PromQL proxying is a
higher risk. A client that hits the 60/min metrics limit before the 100/min global limit
receives a 429 specifically from the metrics proxy handler.

**Test for 60/min metrics limit:** Send 61 allowlisted metrics queries in under 60 seconds
from the same IP → 61st query returns 429.

**Test for 100/min global limit:** Send 101 non-metrics requests (e.g. `GET /api/scenarios`)
in under 60 seconds → 101st returns 429.

### 11.2 Observatory API Authentication

- Mutating endpoints: `Authorization: Bearer $DEMO_TOKEN`, checked via constant-time `hmac.Equal`. 401 on fail.
- `$DEMO_TOKEN`: 32-byte random secret in `docker-compose.observatory.yml`.

### 11.3 Admin Endpoint Authentication

- `POST /admin/circuit-breakers/reset`: `Authorization: Bearer $ADMIN_TOKEN`, checked via `hmac.Equal`. 401 on fail.
- `$ADMIN_TOKEN`: separate 32-byte random secret, distinct from `$DEMO_TOKEN`.
- Set in `docker-compose.observatory.yml` on both gateway and Observatory containers.
- Observatory passes it as the `Authorization` header when calling `gateway:9090`.
- **Never exposed in public API responses or SSE events.**

### 11.4 PII and Span Attribute Sanitization

```go
// Hash before setting as span attribute
func hashAttr(value string) string {
    h := sha256.Sum256([]byte(value))
    return hex.EncodeToString(h[:])[:8]
}
```

- `auth.user_id`: `hashAttr(jwt_sub_claim)` — never raw user ID
- `ratelimit.client_key`: `hashAttr(user_id_or_ip)` — never raw IP
- `http.path`: route template (`/api/users`) not entity path (`/api/users/42`) — use `route.Path` from `RouteConfig`

**SSE event sanitization:** Before emitting any event, Observatory strips from `attrs` values:
- Strings matching `^eyJ[A-Za-z0-9+/]+\.[A-Za-z0-9+/]+\.[A-Za-z0-9+/]+$` (JWT pattern)
- String-equal matches to `$ADMIN_TOKEN` or `$DEMO_TOKEN`

### 11.5 Admin Endpoint Network Isolation

- `:9090` not in `ports:` in any Compose file
- Caddy does not proxy `gateway:9090`
- Verify post-deploy: `curl localhost:9090/admin/circuit-breakers/reset` from VPS host → connection refused

### 11.6 Grafana Embedding

```ini
# monitoring/grafana/provisioning/grafana.ini
[security]
allow_embedding = true
content_security_policy = false
```

> `content_security_policy = false` is a blunt setting suitable for a dedicated demo
> VPS accessible only by the owner. It is **not** the default production posture and is
> **not** appropriate for multi-tenant or shared Grafana instances. If a more surgical
> fix is needed for a future Grafana version, set `content_security_policy_template` to
> include the demo subdomain as an explicit `frame-ancestors` source instead of
> disabling CSP entirely.

Verify cross-subdomain iframe loading in Chrome and Firefox against the real VPS before
building the Observability page Traces tab (M4 step 5 in §12). If embedding remains
blocked despite config, implement the fallback button (§9.5) and document the decision.

### 11.7 Prometheus Proxy Allowlist

See §8.7. Prevents arbitrary PromQL, label enumeration, and unbounded queries from
browser-side Observatory UI.

---

## 12. Implementation Order

### Milestone Overview

```
M1 (OTel + Exemplars + Metrics)
  └─► M2 (Observatory Backend Core)
         ├─► M3 (All 9 Scenarios + k6)   ──────────────────────┐
         └─► M4 (Frontend — 4 pages)     ──────────────────────┤
               └─► M5 (Hardening + Dashboards 2–4)             │
                     └─► M6 (Deploy + Demo Video)    ◄──────────┘
```

M3 and M4 both depend on M2 but are independent of each other (run in parallel).
M5 needs both M3 and M4 done. M6 needs M5 done.

### Recommended vertical slice before full build

Before completing all nine scenarios or the full frontend, validate the entire
observability chain end-to-end in this order:
1. `make observatory-up` → one request → trace appears in Grafana Tempo
2. One exemplar dot → click → trace opens
3. One SSE event with `trace_id` in the stream
4. One `POST /api/reset` → `{"status":"clean"}`

Only then build the remaining scenarios and frontend. This catches infrastructure
problems (Collector config, Grafana provisioning, Docker networking) before they
contaminate scenario testing.

### M1 — OTel, Exemplars, Metrics (Weeks 1–2)

**Goal:** Every gateway request produces a complete OTel trace in Tempo; exemplar dot on
Grafana latency panel links to that trace; `gateway_circuit_state` gauge and admin reset
endpoint are live.

Steps:
1. `internal/telemetry/telemetry.go` — `Init()` with no-op fallback; 5s OTLP connection timeout; 10s export timeout; log once on first failure, not on every failed export
2. Outer chain spans (tracing, router, auth, ratelimiter) with all §5.4 attributes
3. Inner chain spans (retry, loadbalancer, circuitbreaker, upstream) with all §5.4 attributes
4. `gateway_circuit_state{target}` gauge + `Registry.Reset()` + admin server on `:9090`
5. `ObserveWithExemplar` on `gateway_request_duration_seconds` (§6.2 Steps 1–2)
6. `docker-compose.observatory.yml` skeleton: Tempo + OTel Collector; gateway env vars; pinned image tags
7. Grafana: `tempo.yaml` datasource (`uid: tempo`); `prometheus.yaml` with `exemplarTraceIdDestinations`; `grafana.ini` with `allow_embedding = true`
8. Prometheus: `exemplar_storage: true`; `scrape_protocols` for OpenMetrics (§6.2 Steps 3–4)
9. Dashboard 1 (`gateway-overview.json`): p99 panel with exemplars enabled

**Exit criterion:** Kill `user-service-2`; send one request; money trace visible in Grafana
Explore (`cb.state=open`, zero upstream span, < 5ms total). Exemplar dot on p99 panel;
clicking it opens the same trace. `make test`, `make test-race`, `make lint` all green.
**Do not proceed to M2 until this exit criterion passes end-to-end.**

### M2 — Observatory Backend Core (Week 3)

**Goal:** Observatory runs, executes happy-path and circuit-breaker-recovery via API,
streams events, resets cleanly.

Steps:
1. Observatory skeleton: health endpoint with `spec_version: "2.2"` and `jwt_valid`/`toxiproxy_ready` fields
2. JWT startup: verify exact login path against `services/user-service/`; implement B4 with correct path
3. Toxiproxy idempotent proxy creation at startup
4. Scenario YAML loader + API endpoints
5. k6 Docker runner: pre-pull (B1); explicit network mode (R4); `setup()` gateway health check
6. Chaos orchestration: `at_seconds` timing; HTTP calls to service chaos endpoints
7. Docker SDK `ContainerLogs` + `stdcopy.StdCopy()` → JSON parse → SSE broadcast (B5); include unit test for Docker multiplexed stream before calling M2.5 done
8. Prometheus query proxy with allowlist (separate 60/min metrics limit; 100/min global — §11.1)
9. Reset procedure (§8.5)

**Exit criterion:** Run circuit-breaker-recovery at Moderate via API; SSE shows `circuit_open` then `circuit_closed`; reset returns `{"status":"clean"}` within 15s three times consecutively.

### M3 — All 9 Scenarios (Week 4)

**Goal:** All 9 scenario YAMLs and k6 scripts complete; each runs and resets cleanly.

Order: S1 (done in M2) → S2 → S3 → S4 → S5 → S6 (done in M2) → S7 → S8 → S9.

**Exit criterion:** Sequential run of all 9 scenarios with reset between each; all produce expected signals; all reset clean. `make scenario-list` prints all 9 names.

### M4 — Frontend (Weeks 5–6)

**Goal:** React frontend at `web:3001`; all 4 pages functional against Observatory and Grafana.

Steps:
1. Scaffold: Vite + React + TS strict + Tailwind tokens + shadcn primitives + `cn()`
2. Landing: static pipeline SVG + stat counters (Prometheus-driven via Observatory proxy)
3. About: interactive pipeline diagram + ADR drawers + decision cards
4. Observatory page: scenario picker + run controls + system status + SSE event feed + 4 Recharts panels + trace shortcuts bar
5. **Before Tab 2:** Verify Grafana embedding in Chrome and Firefox on real VPS (B2). If blocked, implement fallback button.
6. Observability: Metrics tab (Grafana `d-solo` iframes) + Traces tab (Grafana Explore embed or fallback) + Logs tab (SSE log stream)
7. Pipeline dot animation (Prometheus-driven)
8. `web/Dockerfile` (nginx alpine serving `/dist`)

**Exit criterion:** A stranger opens Observatory page, picks S6 at Severe, hits RUN, and follows all 8 steps from §1 in under 90 seconds without instructions.

### M5 — Hardening + Dashboards 2–4 (Week 7)

**Goal:** All security requirements enforced; Dashboards 2–4 provisioned; 10 consecutive resets pass.

Steps:
1. Grafana Dashboards 2, 3, 4 as provisioned JSON
2. Security: constant-time token checks; separate 60/min metrics and 100/min global rate limits; SSE sanitization; Compose `mem_limit`/`cpus` on observatory, web, toxiproxy
3. Sequential all-9-scenarios run + 10 consecutive resets

**Exit criterion:** All 4 dashboards auto-provisioned; all security tests pass; 10 consecutive resets clean; all make targets green.

### M6 — Deploy + Demo Video (Weeks 8–9)

**Goal:** Full stack live at 4 HTTPS subdomains on production VPS; 2-minute demo video recorded.

Steps:
1. VPS upgrade to 8 GB / 4 vCPU (required for M6; local dev may use smaller machines)
2. `make observatory-up` on clean VPS; verify all containers healthy within 90s
3. Caddy: add demo and observatory subdomains; verify TLS on all four
4. Live stack verification: run S6 end-to-end against `https://` URLs
5. Rehearse demo script ≥5 times
6. Record 2-minute video
7. Update README: Phase 9 section, live URLs, video embed

**Exit criterion:** All §17 success criteria checked on production VPS. Video recorded and linked from README.

---

## 13. Project Structure

```text
irongate/
├── cmd/
│   ├── gateway/                         unchanged
│   └── observatory/
│       ├── main.go
│       ├── api.go
│       ├── runner.go
│       ├── chaos.go
│       ├── events.go
│       ├── events_test.go               Docker stream demux unit test (required)
│       ├── metrics.go
│       ├── reset.go
│       ├── scenarios.go
│       └── toxiproxy.go
├── web/
│   ├── src/
│   │   ├── pages/
│   │   │   ├── Landing.tsx
│   │   │   ├── About.tsx
│   │   │   ├── Observatory.tsx
│   │   │   └── Observability.tsx
│   │   ├── components/
│   │   │   ├── ui/                      shadcn primitives (repo-owned copies)
│   │   │   │   ├── alert-dialog.tsx
│   │   │   │   ├── badge.tsx
│   │   │   │   ├── button.tsx
│   │   │   │   ├── dialog.tsx
│   │   │   │   ├── select.tsx
│   │   │   │   ├── separator.tsx
│   │   │   │   ├── sheet.tsx
│   │   │   │   ├── tabs.tsx
│   │   │   │   └── tooltip.tsx
│   │   │   ├── PipelineDiagram/
│   │   │   │   ├── PipelineDiagram.tsx
│   │   │   │   ├── PipelineNode.tsx
│   │   │   │   └── usePipelineMetrics.ts
│   │   │   ├── EventFeed/
│   │   │   │   ├── EventFeed.tsx
│   │   │   │   ├── EventRow.tsx
│   │   │   │   └── EventFilter.tsx
│   │   │   ├── MetricPanels/
│   │   │   │   ├── RequestRatePanel.tsx
│   │   │   │   ├── LatencyPanel.tsx
│   │   │   │   ├── CircuitStateTimeline.tsx
│   │   │   │   └── RateLimitPanel.tsx
│   │   │   ├── ScenarioPicker/
│   │   │   │   ├── ScenarioPicker.tsx
│   │   │   │   ├── ScenarioCard.tsx
│   │   │   │   ├── IntensitySelector.tsx
│   │   │   │   └── ActiveScenarioBanner.tsx
│   │   │   ├── SystemStatus.tsx
│   │   │   ├── RunControls.tsx
│   │   │   └── TraceShortcutsBar.tsx
│   │   ├── hooks/
│   │   │   ├── useEventStream.ts
│   │   │   ├── usePrometheusQuery.ts
│   │   │   ├── useScenario.ts
│   │   │   └── useSystemStatus.ts
│   │   ├── lib/
│   │   │   ├── utils.ts                 cn() — tailwind-merge + clsx
│   │   │   ├── api.ts                   Observatory API client
│   │   │   └── prometheus.ts            Prometheus query helpers
│   │   └── types/
│   │       ├── events.ts
│   │       └── scenarios.ts
│   ├── package.json                     exact semver pins
│   ├── tailwind.config.js
│   ├── vite.config.ts
│   └── Dockerfile
├── scenarios/
│   ├── happy-path.yaml
│   ├── auth-wall.yaml
│   ├── rate-limit-storm.yaml
│   ├── single-replica-death.yaml
│   ├── upstream-5xx-retry.yaml
│   ├── circuit-breaker-recovery.yaml
│   ├── cascading-failure.yaml
│   ├── redis-impaired.yaml
│   ├── latency-injection.yaml
│   └── k6/
│       ├── lib/common.js
│       ├── happy-path.js
│       ├── auth-wall.js
│       ├── rate-limit-storm.js
│       ├── rate-limit-storm-many-keys.js
│       ├── single-replica-death.js
│       ├── upstream-5xx-retry.js
│       ├── circuit-breaker-recovery.js
│       ├── cascading-failure.js
│       ├── redis-impaired.js
│       └── latency-injection.js
├── otel/
│   └── collector-config.yaml
├── monitoring/
│   ├── prometheus/
│   │   └── prometheus.yml
│   └── grafana/
│       ├── dashboards/
│       │   ├── gateway-overview.json
│       │   ├── resilience.json
│       │   ├── rate-limiting.json
│       │   └── scenario-view.json
│       └── provisioning/
│           ├── grafana.ini             allow_embedding = true
│           ├── datasources/
│           │   ├── prometheus.yaml
│           │   └── tempo.yaml
│           └── dashboards/
│               └── dashboards.yaml
├── docs/
│   └── phase9-planning/
│       ├── DECISIONS_LOCK.md
│       ├── PHASE9_CHAOS_OBSERVATORY_SPEC_v2.2.md
│       └── PHASE9_IMPLEMENTATION_PLAN_v1.2.md
├── internal/
│   ├── telemetry/
│   │   └── telemetry.go
│   └── ...                              all existing packages unchanged
├── docker-compose.yml                   unchanged
├── docker-compose.observatory.yml       Phase 9 overlay (pinned image tags)
├── Makefile
└── ...
```

---

## 14. Makefile Targets

```makefile
# Existing — unchanged
make build
make test
make test-race
make lint
make coverage

# Observatory (includes k6 pre-pull per B1)
make observatory-up      # docker pull grafana/k6:<tag> && \
                         # docker-compose -f docker-compose.yml \
                         #   -f docker-compose.observatory.yml up -d
make observatory-down
make observatory-reset   # POST /api/reset
make observatory-logs    # tail observatory container logs

# Frontend
make web-dev             # cd web && npm run dev  (Vite on :5173)
make web-build           # cd web && npm run build
make web-lint            # cd web && npm run lint

# k6 direct (bypass Observatory — for debugging)
make k6-happy-path
make k6-circuit-breaker
make k6-rate-limit

# Convenience
make scenario-list       # parse scenarios/*.yaml and print names
make traces-open         # open Grafana Explore (Tempo) in browser
make grafana-open        # open grafana.yourdomain.com in browser
make demo-open           # open demo.yourdomain.com in browser
make demo                # observatory-up + wait-healthy + demo-open + grafana-open
```

---

## 15. Deployment

### Public Subdomains — Complete TLS Scope

This section describes the post-M6 Phase 9 production overlay. Until M6 ships, the
current deployment remains the narrower Phase 8 layout documented in
[`deploy/README.md`](../../deploy/README.md).

The following four subdomains are exposed publicly via Caddy. TLS certificates are
issued automatically by Let's Encrypt for all four. No others.

| Subdomain | Internal target | Auth |
|-----------|----------------|------|
| `gateway.yourdomain.com` | `gateway:8080` | JWT per route (existing) |
| `grafana.yourdomain.com` | `grafana:3000` | Grafana login |
| `demo.yourdomain.com` | `web:3001` | Public |
| `observatory.yourdomain.com` | `observatory:9000` | Public GET; Bearer token POST |

**Not exposed publicly:** Redis (`:6379`), Prometheus (`:9090`), Tempo (`:3200`/`:4317`),
OTel Collector (`:4317`/`:4318`), Toxiproxy (`:8474`/`:6380`), gateway admin (`:9090`).

### Caddyfile additions

```
demo.yourdomain.com {
    reverse_proxy web:3001
}

observatory.yourdomain.com {
    reverse_proxy observatory:9000
}
```

The existing `gateway.yourdomain.com` and `grafana.yourdomain.com` blocks are unchanged.

### Access Control Summary

| What | Public | Requires |
|------|--------|---------|
| `demo.yourdomain.com` (full UI) | ✅ | — |
| Observatory GET endpoints | ✅ | — |
| Observatory POST `/run`, `/reset` | ❌ | `Bearer $DEMO_TOKEN` |
| Grafana UI | ❌ | Grafana login |
| Gateway admin `:9090` | ❌ | Internal Docker network + `Bearer $ADMIN_TOKEN` |

---

## 16. VPS Sizing

| Container | Approx RAM |
|-----------|-----------|
| Gateway | 50 MB |
| 5 upstream services | 150 MB |
| Redis | 50 MB |
| Prometheus | 300 MB |
| Grafana | 200 MB |
| Tempo | 300 MB |
| OTel Collector | 100 MB |
| Toxiproxy | 30 MB |
| Observatory | 50 MB |
| Web (nginx) | 20 MB |
| k6 (during run) | 200 MB |
| OS + Docker overhead | 500 MB |
| **Total peak** | **~1.95 GB** |

**Required for M6 (production deployment):** 8 GB / 4 vCPU DigitalOcean droplet (~$48/month).
**Local dev (M1–M5):** A laptop with 8+ GB RAM and Docker Desktop is sufficient.

Snapshot and destroy when not actively demoing. Recreate from snapshot in < 5 minutes.

---

## 17. Success Criteria

### Functional
- [ ] OTel spans appear in Tempo for all 9 span types in §5.3
- [ ] Exemplar dots on `gateway_request_duration_seconds` p99 panel link to correct Tempo traces
- [ ] All 9 scenarios run end-to-end without manual intervention
- [ ] Reset returns `{"status":"clean"}` in under 15 seconds
- [ ] SSE event feed shows correct types and colors for all scenarios
- [ ] `trace_id` in SSE events matches actual Tempo trace IDs (verified for `circuit_open`)
- [ ] `gateway_circuit_state` gauge updates on every CB state transition
- [ ] CB flush via admin endpoint returns all `gateway_circuit_state` gauges to 0
- [ ] Toxiproxy toxics add/remove cleanly via Observatory API
- [ ] `/api/health` returns `spec_version: "2.2"`, `jwt_valid: true`, `toxiproxy_ready: true`

### Demo Quality
- [ ] S6 runs to full automatic recovery — no manual steps
- [ ] Stranger follows all 8 steps from §1 in under 90 seconds (verified with a test user)
- [ ] Event feed and Circuit State panel change within 1 second of each other during CB open
- [ ] Money trace: `cb.state=open`, zero upstream span, < 1ms total — visible in Tempo
- [ ] 10 consecutive resets all return `{"status":"clean"}`
- [ ] 2-minute demo video recorded with zero ad-lib — system narrates itself

### Observability
- [ ] All 4 Grafana dashboards provisioned automatically with no manual steps
- [ ] Dashboard 4 (Scenario View) legible when screen-shared at 1080p
- [ ] Logs tab: clicking `req=X` opens correct Tempo trace search in Grafana Explore
- [ ] Traces tab: scenario shortcuts show correct traces from most recent run

### Production
- [ ] `make observatory-up` starts full stack on clean VPS in under 90 seconds
- [ ] All 4 subdomains (`gateway`, `grafana`, `demo`, `observatory`) have valid TLS
- [ ] Observatory mutating endpoints reject unauthenticated requests with 401
- [ ] Observatory metrics proxy rejects non-allowlisted queries with 403
- [ ] 60/min metrics rate limit fires on 61st metrics query
- [ ] Admin CB endpoint unreachable from VPS host (`curl localhost:9090` → refused)

### Demo Video Script (2 minutes, rehearsed ≥5 times before recording)

| Time | Action |
|------|--------|
| 0:00–0:10 | Open `demo.yourdomain.com`. Show live stat counters updating. |
| 0:10–0:15 | Navigate to Observatory. Select S6, Severe, 120s. |
| 0:15–0:20 | Hit RUN. Say: "I'm going to kill one upstream instance." |
| 0:20–0:50 | Watch: green events → red `circuit OPEN` → collapsed 503 events. |
| 0:50–1:00 | Click `[View Trace →]` on `circuit_open` event. Show Tempo: `cb.state=open`, zero upstream span, < 1ms total. |
| 1:00–1:10 | Return to Observatory. Click exemplar dot on Grafana latency panel. Same trace opens. |
| 1:10–1:30 | Watch: `half-open probe sent` → `circuit CLOSED` → green traffic resumes. |
| 1:30–1:40 | Click RESET ALL; confirm. Show `{"status":"clean"}`. |
| 1:40–1:50 | Navigate to Observability → Traces tab. Show Grafana Explore with trace list. |
| 1:50–2:00 | Close with one sentence. Stop recording. |

---

## 18. What This Delivers in an Interview

When an interviewer opens `demo.yourdomain.com` and you say *"pick any scenario and run it"*
— that sentence alone is different from every other junior engineer they have interviewed.

When they pick S6, watch the event feed, click a 503 event, and see a trace showing
a request rejected in under 1ms with `cb.state=open` and zero upstream span — they are
not looking at a portfolio project.

They are looking at someone who built, instrumented, and operationalized infrastructure
sophisticated enough to demonstrate itself.

That is the interview you want.

---

## Appendix A — Changes from v2.1

- **§ header/preamble reworded.** "Nothing changes gateway behavior" replaced with precise scope statement: no changes to routing/auth/rate-limit/retry/CB semantics; additions are observability hooks and admin endpoints only.
- **§3.3 expanded.** Four gateway additions now listed explicitly (OTel spans, `gateway_circuit_state` gauge, `ObserveWithExemplar`, admin server), each with a pointer to the relevant section. v2.1 listed only two.
- **§3.3 exemplar addition added.** `ObserveWithExemplar` on `gateway_request_duration_seconds` was missing from the §3.3 gateway changes list — implementers reading only §3.3 would skip it. Now explicitly listed with a pointer to §6.
- **§5.3 money trace `http.path` annotated.** Added `[route template]` comment to the root span in the trace waterfall example, reinforcing the §5.4 rule inline where it matters most.
- **§5.4 root span `http.path` row tightened.** Value column now states explicitly: "Route template only — e.g. `/api/users`, never `/api/users/42`. Use `route.Path` from `RouteConfig`, not `req.URL.Path`."
- **§8.2 health endpoint enriched.** `GET /api/health` now returns `{"status","spec_version","jwt_valid","toxiproxy_ready"}` for deployed-demo traceability.
- **§8.8 JWT bootstrap path locked.** Observatory now uses `POST http://gateway:8080/api/users/login` with an empty body. The underlying service route remains `POST /users/login`, but no separate `DEMO_USER` / `DEMO_PASS` contract is introduced.
- **§11.1 rate limits split and clarified.** v2.1 had 60 req/min in §8.7 and 100 req/min in §11.1 with no explanation of which applied where; M5.2 acceptance used the wrong value (110 GETs against 60/min limit). Now §11.1 has a single table defining both limits, which routes they apply to, and separate test commands for each.
- **§11.6 CSP note added.** `content_security_policy = false` is flagged as a blunt setting for dedicated demo use only; a more surgical alternative is described for future-proofing.
- **§12 M1 added OTLP timeout spec.** `Init()` now specifies 5s connection timeout and 10s export timeout, and "log once on first failure, not every export" — prevents unbounded goroutines and log spam during local dev without a Collector.
- **§12 VPS sizing clarified.** 8 GB / 4 vCPU is required for M6 (production); local dev on M1–M5 does not require the upgraded VPS.
- **§12 vertical slice recommendation added.** Explicit "validate one trace → one exemplar → one SSE event → one reset before building all scenarios" step documented before the full milestone sequence.
- **§15 subdomain checklist.** v2.1 had inconsistent subdomain counts across sections. §15 now has a single canonical table of all four public subdomains (`gateway`, `grafana`, `demo`, `observatory`), their internal targets, and auth requirements. All other sections cross-reference this table.
- **§17 production success criteria expanded.** Added: `/api/health` returns `spec_version: "2.2"`; 60/min metrics rate limit fires on 61st query; 4 named subdomains have valid TLS.
- **§3.2 image pinning added.** Pinned image versions table added for Tempo, OTel Collector, Toxiproxy, k6. `:latest` is explicitly prohibited.
- **§9.1 package.json pinning added.** Exact semver pins required, not ranges.
- **Canonical path fixed.** This document now declares its real repo path under `docs/phase9-planning/` instead of a hypothetical root filename.

---

## Appendix B — Closed Decisions (traceability only)

All decisions are resolved and incorporated into the spec body. Do not re-open.

| ID | Decision | Where in spec |
|----|----------|--------------|
| B1 | Pre-pull `grafana/k6:<pinned-tag>` in `make observatory-up` | §8.8, §14 |
| B2 | `allow_embedding = true` + `content_security_policy = false` in `grafana.ini`; verify cross-subdomain iframe before M4 Traces tab | §11.6, §12 M4 |
| B3 | Add `gateway_circuit_state{target}` gauge (0/1/2); implement in M1 | §3.3, §8.7, §10 Dashboard 2 |
| B4 | Observatory mints its demo JWT via `POST http://gateway:8080/api/users/login`; 23h refresh; fallback to `DEMO_JWT` | §8.8 |
| B5 | Docker SDK `ContainerLogs(Follow: true)` + `stdcopy.StdCopy()`; one JSON object per line | §8.4 |

---

*Status: Approved Phase 9 planning spec*
*Spec Version: 2.2 — supersedes v2.1*
*Canonical path: `docs/phase9-planning/PHASE9_CHAOS_OBSERVATORY_SPEC_v2.2.md`*
*Current runtime stays Phase 8 until these deliverables ship on `main`*
