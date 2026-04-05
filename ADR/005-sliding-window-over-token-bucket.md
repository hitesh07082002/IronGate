# ADR-005: Sliding Window as Core Rate Limiting Algorithm

**Status:** Accepted
**Date:** April 2026

## Context

Two common rate limiting algorithms: sliding window and token bucket. Both are well-understood, both can be implemented atomically with Redis Lua scripts.

- **Sliding window:** "100 requests per 60 seconds" means exactly that. Count requests in a moving time window using a Redis sorted set.
- **Token bucket:** Bucket fills at a steady rate, allows bursts up to bucket size. More complex mental model, more config parameters (`requests_per_second` + `burst`).

Building both algorithms as a junior engineer working part-time adds scope without proportional learning value. The real learning is in the Redis Lua atomicity, not in implementing two algorithms.

## Decision

Sliding window is the core implementation. Token bucket is deferred to a stretch goal.

Config uses `requests` (max count) + `window` (duration) + `strategy: "sliding_window"`.

## Consequences

- Simpler config: two parameters instead of three.
- Simpler mental model for users: "100 requests per 60 seconds."
- Focus implementation time on getting the Redis Lua script right, testing edge cases, and handling Redis failures.
- Token bucket can be added later as a second strategy behind the same `strategy:` config field.
- The `strategy` field exists from day 1 so adding token bucket later is just a new implementation, not a config schema change.

## Alternatives Considered

**Token bucket as core:** Allows bursts, which is useful for real-world traffic. But adds complexity in config and implementation. AWS and Stripe use token bucket, but Cloudflare and GitHub use sliding window.

**Both from day 1:** Maximum feature depth but high scope risk. The real value is demonstrating one algorithm well, with proper atomicity, failure handling, and tests.
