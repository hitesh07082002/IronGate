# ADR-001: Two-Tier Middleware Pipeline

**Status:** Accepted
**Date:** April 2026

## Context

The gateway needs middleware for tracing, routing, auth, rate limiting, load balancing, circuit breaking, and retry. The naive approach is a flat linear chain where each middleware wraps the next.

The problem: retry needs to re-invoke the load balancer to pick a different target on each attempt. Circuit breaker needs to know the specific target (host:port) to track state per-target. In a flat chain where load balancing happens after circuit breaking, these interactions are impossible.

## Decision

Split the pipeline into two tiers:

**Outer chain** (`http.Handler` middleware): Tracing, Router, Auth, RateLimiter, Proxy. These are request-level concerns that run once per incoming request.

**Inner chain** (`http.RoundTripper` decorators): Retry, LoadBalancer, CircuitBreaker, HTTP Transport. These are transport-level concerns that may execute multiple times per request (on retry).

The Proxy uses `httputil.ReverseProxy` with a custom `Transport` field that points to the inner chain.

## Consequences

- Retry can re-invoke the load balancer on each attempt to pick a different target.
- Circuit breaker sees the specific target that load balancer selected.
- The project structure reflects the split: `internal/middleware/` vs `internal/transport/`.
- Slightly more complex wiring in `main.go`, but each layer is simpler to reason about.
- This is the same pattern Traefik and other production gateways use.

## Alternatives Considered

**Flat chain:** Simpler to wire up, but makes retry+LB and CB+target interactions impossible without hacks. Would require passing state between middleware via headers or context, making the code harder to follow.
