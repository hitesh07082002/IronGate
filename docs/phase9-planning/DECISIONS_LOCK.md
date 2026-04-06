# Phase 9 Decisions Lock

> Status: Locked for implementation
> Scope: Applies to the planned Phase 9 Chaos Observatory work only
> Canonical path: `docs/phase9-planning/DECISIONS_LOCK.md`
> Paired docs:
> - `docs/phase9-planning/PHASE9_CHAOS_OBSERVATORY_SPEC_v2.2.md`
> - `docs/phase9-planning/PHASE9_IMPLEMENTATION_PLAN_v1.2.md`

This file exists to stop drift while Phase 9 is being built. If the spec or the
implementation plan is ambiguous, this file wins until a deliberate follow-up PR changes it.

## Locked Decisions

1. **Current-runtime boundary**
   Phase 8 on `main` remains the shipped baseline until Phase 9 lands. For live behavior,
   current runtime docs still come from `ARCHITECTURE.md`, `PROGRESS.md`, and `deploy/README.md`.

2. **JWT bootstrap path**
   Observatory mints and refreshes its demo JWT via `POST http://gateway:8080/api/users/login`
   with an empty body.

   Rationale:
   - this is the stable gateway-facing contract already present in the repo
   - it avoids coupling the docs to a specific `user-service-*` container name
   - the underlying user-service route remains `POST /users/login`, but Observatory does not call it directly

3. **No `DEMO_USER` / `DEMO_PASS` contract**
   Phase 9 does not invent a second login credential contract. Observatory uses the existing
   login route above, or a static `DEMO_JWT` override for local development.

4. **Config contract stays YAML-first**
   Gateway Redis wiring continues to come from `configs/gateway.yaml`, which should resolve:

   `redis.address: "${REDIS_ADDR:-redis:6379}"`

   The Phase 9 overlay sets `REDIS_ADDR=toxiproxy:6380`. The base stack keeps the default.

5. **Repo layout for Prometheus / Grafana assets**
   New observability assets extend the existing repo structure, not a parallel tree:
   - `monitoring/prometheus/...`
   - `monitoring/grafana/provisioning/...`
   - `monitoring/grafana/dashboards/...`

6. **Public demo surface**
   The planned Phase 9 production overlay exposes four public subdomains only:
   - `gateway.yourdomain.com`
   - `grafana.yourdomain.com`
   - `demo.yourdomain.com`
   - `observatory.yourdomain.com`

   Until M6 ships, the current production deployment remains the Phase 8 layout documented in `deploy/README.md`.

7. **Demo-only security relaxations**
   `allow_embedding = true` and `content_security_policy = false` are allowed only for a
   dedicated demo Grafana on a dedicated demo VPS. They are not the default production
   hardening posture and must stay labeled as demo-only in all docs.

8. **Version pin policy**
   Pinned image tags and tool versions are bumped only through an explicit PR that reruns:
   - `make observatory-up`
   - the relevant smoke / verification path
   - the demo walkthrough or replay script

9. **Admin surface**
   The gateway admin server stays internal-only on `:9090`, never published in Compose,
   and never proxied by Caddy.
