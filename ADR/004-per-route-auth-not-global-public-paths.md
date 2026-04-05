# ADR-004: Per-Route `auth_required` Instead of Global `public_paths`

**Status:** Accepted
**Date:** April 2026

## Context

Auth config can be defined two ways:

1. **Global `public_paths` list** in the `auth:` section: `public_paths: ["/health", "/api/users/login"]`
2. **Per-route `auth_required` flag**: each route declares `auth_required: true` or `false`

The original design had both. This creates ambiguity: what if a route has `auth_required: true` but its path is also in `public_paths`? Which wins?

## Decision

Use only `auth_required` per route. Remove the global `public_paths` list entirely.

Auth middleware reads the `auth_required` flag from the route config stored in `context.Context` (set by Router). If `auth_required` is false, auth passes through without checking for a token.

## Consequences

- One source of truth for auth behavior: the route config.
- No ambiguity. Each route explicitly declares whether it needs auth.
- Auth middleware is simpler: read one boolean from context, act on it.
- Adding a new public endpoint means setting `auth_required: false` on that route, which is where you're already defining the route anyway.

## Alternatives Considered

**Both systems:** More flexible but creates conflicting config. The `public_paths` list can drift out of sync with route definitions. In a config-driven system, split config = split bugs.
