# ADR-008: Standard `func(http.Handler) http.Handler` Middleware Interface

**Status:** Accepted
**Date:** April 2026

## Context

Go has no official middleware interface, but the community has converged on a de facto standard:

```go
type Middleware func(http.Handler) http.Handler
```

This pattern is used by the Go stdlib (`http.StripPrefix`, `http.TimeoutHandler`), Chi, Alice, and most Go web frameworks.

Alternatives include custom interfaces with `Process(req, next)` methods or framework-specific patterns.

## Decision

Use `func(http.Handler) http.Handler` for all outer-chain middleware. Middleware is applied in reverse order so the first-listed is outermost.

```go
// Building the chain
handler := proxy  // innermost
handler = rateLimiter(handler)
handler = auth(handler)
handler = router(handler)
handler = tracing(handler)  // outermost
```

## Consequences

- Standard pattern. Anyone reading the code recognizes it immediately.
- Composable. Middleware can be reordered, added, or removed without changing other components.
- Compatible with Go stdlib and third-party middleware (logging, CORS, etc.).
- No custom framework to learn. The middleware chain is just nested function calls.
- The inner chain uses `http.RoundTripper` instead, which is a different Go standard interface. Both are well-understood patterns.

## Alternatives Considered

**Custom `Middleware` interface with `Handle(ctx, req, next)`:** More explicit, but non-standard. Forces every middleware author to learn a custom interface. Not compatible with third-party middleware.

**Framework-specific patterns (Gin, Echo):** Ties the project to a framework. IronGate uses stdlib `net/http` to demonstrate understanding of Go fundamentals.
