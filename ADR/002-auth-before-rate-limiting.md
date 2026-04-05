# ADR-002: Auth Runs Before Rate Limiting

**Status:** Accepted
**Date:** April 2026

## Context

Rate limiting needs a client identifier to track request counts. The two candidates are: IP address (available before auth) or authenticated user ID (available after auth).

If rate limiting runs before auth, unauthenticated requests (invalid/missing JWT) still count against the rate limit. An attacker could send thousands of requests with garbage tokens, burning the rate limit quota for the IP address. Legitimate users sharing that IP (corporate NAT, VPN) get locked out.

## Decision

Auth runs before RateLimiter in the outer middleware chain:

```
[Tracing] → [Router] → [Auth] → [RateLimiter] → [Proxy]
```

Rate limiting uses the authenticated user ID (`X-User-ID` from JWT claims) as the primary key. Falls back to client IP for routes with `auth_required: false`.

## Consequences

- Unauthenticated requests get rejected at the auth layer (401) before touching the rate limiter.
- Rate limits are per-user, not per-IP. More accurate, less collateral damage.
- Routes with `auth_required: false` (like `/health`) still get rate-limited by IP.
- Auth middleware must be fast. A slow JWT validation adds latency to every request. HMAC-SHA256 verification is <1ms, so this is fine.

## Alternatives Considered

**Rate limiting before auth (by IP):** Simpler, but attackers can exhaust rate limits for shared IPs. Also prevents per-user rate limiting entirely.
