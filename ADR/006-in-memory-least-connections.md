# ADR-006: In-Memory Least-Connections Counter

**Status:** Accepted
**Date:** April 2026

## Context

The least-connections load balancing strategy needs to track the number of active (in-flight) requests per target. This counter could live in Redis (distributed) or in-memory (local).

## Decision

Use an in-memory atomic counter (`sync/atomic` int64 per target). No Redis.

## Consequences

- Simple. No network round-trip to check connection counts.
- No Redis dependency for load balancing (Redis is only for rate limiting).
- Accurate for a single-instance gateway, which is what IronGate is.
- Would not work for a multi-instance gateway (each instance has its own counters). That's fine. IronGate is a single-instance deployment.
- Goroutine-safe via `atomic.AddInt64` for increment/decrement.

## Alternatives Considered

**Redis-backed counter:** Accurate across multiple gateway instances, but adds latency to every request (Redis round-trip before forwarding) and makes load balancing depend on Redis availability. For a single-instance gateway, this is unnecessary complexity.

**Note:** This is a great interview talking point. "I chose in-memory because IronGate is single-instance. If I needed multi-instance, I'd use Redis or a shared state store, but the latency cost of a Redis call per-request for connection counting isn't worth it for this deployment model."
