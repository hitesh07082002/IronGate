# ADR-003: Fail-Open for Rate Limiting When Redis Is Down

**Status:** Accepted
**Date:** April 2026

## Context

Rate limiting relies on Redis for distributed counters. If Redis becomes unreachable (network blip, Redis restart, OOM kill), the rate limiter must choose: reject all requests (fail-closed) or allow all requests through (fail-open).

## Decision

Fail-open. When Redis is unreachable, the rate limiter allows the request through and logs a warning.

## Consequences

- A Redis outage temporarily disables rate limiting. Extra traffic may reach upstream services.
- Legitimate users are never blocked by a Redis blip. Availability is preserved.
- The warning log enables alerting. Operators can detect the degradation quickly.
- This matches how production gateways handle it. Rejecting legitimate traffic due to an infrastructure blip is worse than briefly allowing extra traffic.

## Alternatives Considered

**Fail-closed (reject all):** Safer from an abuse perspective, but a 30-second Redis restart would reject every request. For a portfolio project demonstrating operational thinking, fail-open is the right call. In a real system with compliance requirements (payment processing), you might choose fail-closed for specific routes.

**In-memory fallback counter:** More complex. Requires synchronization, doesn't distribute across gateway instances. Not worth the complexity for a single-instance gateway.
