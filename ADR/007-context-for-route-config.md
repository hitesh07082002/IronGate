# ADR-007: Route Config via `context.Context`

**Status:** Accepted
**Date:** April 2026

## Context

After the Router matches a path to a route, downstream middleware (Auth, RateLimiter, Proxy) needs access to the matched route's config (auth rules, rate limits, targets, retry policy). Three options:

1. Each middleware does its own route lookup from the global config.
2. Router stores the matched config in a custom struct passed through headers.
3. Router stores the matched config in `context.Context` using a typed key.

## Decision

Option 3. Router stores the full `RouteConfig` in the request's `context.Context`:

```go
ctx := context.WithValue(req.Context(), RouteConfigKey, routeConfig)
req = req.WithContext(ctx)
```

Downstream middleware reads it:

```go
routeCfg := req.Context().Value(RouteConfigKey).(*RouteConfig)
```

## Consequences

- Single route lookup per request (in Router), not per middleware.
- Clean dependency flow: middleware depends on context, not on the config package.
- Easy to test: mock the route config by setting it in context directly.
- Thread-safe: context values are immutable per-request.
- Standard Go pattern. Used by `net/http` itself for `ServerContextKey` and by most Go web frameworks.

## Alternatives Considered

**Per-middleware config lookup:** Each middleware looks up the route from the global config using the request path. Duplicates work, couples every middleware to the config package, and requires each middleware to handle "route not found" (which Router already handles).

**Headers:** Encoding config in HTTP headers is fragile, requires serialization, and pollutes the request with internal state.
