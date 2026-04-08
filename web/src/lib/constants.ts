import type { ScenarioDetail } from "@/types/observatory";

export const REPO_URL = import.meta.env.VITE_GITHUB_URL || "https://github.com/hitesh07082002/IronGate";
export const DEMO_TOKEN = import.meta.env.VITE_DEMO_TOKEN || "demo-token";

export const NAV_ITEMS = [
  { to: "/about", label: "About" },
  { to: "/chaos", label: "Observatory" },
  { to: "/observability", label: "Observability" },
] as const;

export const EVENT_FILTERS = [
  { label: "All", value: "all" },
  { label: "Routing", value: "routing" },
  { label: "Auth", value: "auth" },
  { label: "Rate Limit", value: "rate-limit" },
  { label: "Retry", value: "retry" },
  { label: "Circuit", value: "circuit" },
  { label: "System", value: "system" },
] as const;

export const SYSTEM_SERVICES = [
  "gateway",
  "redis",
  "user-service-1",
  "user-service-2",
  "order-service-1",
  "order-service-2",
] as const;

export const ABOUT_DECISIONS = [
  {
    title: "Two-Tier Middleware Pipeline",
    verdict: "Request-level and transport-level concerns stay separate.",
    tradeoff: "Slightly more wiring buys composability for retry, load balancing, and breakers.",
    href: `${REPO_URL}/blob/main/ADR/001-two-tier-pipeline.md`,
  },
  {
    title: "Auth Before Rate Limiting",
    verdict: "Invalid tokens die before they consume rate-limit budget.",
    tradeoff: "JWT validation sits on the hot path, but avoids shared-IP collateral damage.",
    href: `${REPO_URL}/blob/main/ADR/002-auth-before-rate-limiting.md`,
  },
  {
    title: "Fail-Open Rate Limiting",
    verdict: "Availability wins when Redis degrades.",
    tradeoff: "Temporary over-admission is preferred to gateway-wide denial.",
    href: `${REPO_URL}/blob/main/ADR/003-fail-open-rate-limiting.md`,
  },
  {
    title: "Per-Route Auth Config",
    verdict: "Each route declares its own auth boundary.",
    tradeoff: "A little duplication removes global config ambiguity.",
    href: `${REPO_URL}/blob/main/ADR/004-per-route-auth-not-global-public-paths.md`,
  },
  {
    title: "Sliding Window Core Limiter",
    verdict: "Simple mental model beats multi-algorithm sprawl.",
    tradeoff: "No burst semantics yet, but the Redis atomicity is crisp and testable.",
    href: `${REPO_URL}/blob/main/ADR/005-sliding-window-over-token-bucket.md`,
  },
  {
    title: "In-Memory Least Connections",
    verdict: "Single-instance deployment keeps connection counts local and cheap.",
    tradeoff: "Horizontal scale would need shared state later.",
    href: `${REPO_URL}/blob/main/ADR/006-in-memory-least-connections.md`,
  },
  {
    title: "Route Config in Context",
    verdict: "The router resolves once and downstream middleware reads, not re-looks-up.",
    tradeoff: "Typed context plumbing replaces repeated global config access.",
    href: `${REPO_URL}/blob/main/ADR/007-context-for-route-config.md`,
  },
  {
    title: "Standard Middleware Interface",
    verdict: "Boring Go middleware makes the pipeline easy to read and swap.",
    tradeoff: "No framework sugar, but full stdlib interoperability.",
    href: `${REPO_URL}/blob/main/ADR/008-standard-middleware-interface.md`,
  },
] as const;

export const PIPELINE_NODES = [
  {
    id: "router",
    label: "Router",
    color: "stroke-ig-gateway",
    fill: "fill-ig-gateway/10",
    summary: "Matches the route once and pushes config into request context.",
    why: "Everything downstream depends on a stable route contract.",
    failure: "A missed match becomes a clean 404 instead of an ambiguous proxy hop.",
    adr: ABOUT_DECISIONS[6],
  },
  {
    id: "auth",
    label: "Auth",
    color: "stroke-ig-gateway",
    fill: "fill-ig-gateway/10",
    summary: "Validates HS256 JWTs before protected traffic touches upstream services.",
    why: "Protected capacity should not be burned by malformed or missing tokens.",
    failure: "Rejected requests stop at the edge with explicit 401s.",
    adr: ABOUT_DECISIONS[1],
  },
  {
    id: "rate-limit",
    label: "Rate Limit",
    color: "stroke-ig-redis",
    fill: "fill-ig-redis/10",
    summary: "Uses a Redis-backed sliding window to clip excess traffic.",
    why: "The gateway should absorb abuse before it fans out to replicas.",
    failure: "If Redis degrades, the limiter fails open so the gateway stays available.",
    adr: ABOUT_DECISIONS[2],
  },
  {
    id: "retry",
    label: "Retry",
    color: "stroke-ig-gateway",
    fill: "fill-ig-gateway/10",
    summary: "Retries idempotent requests when upstream failures are transient.",
    why: "A brief 5xx burst should not automatically leak to the client.",
    failure: "Once every target is exhausted, retries stop and the client gets a clean error.",
    adr: ABOUT_DECISIONS[0],
  },
  {
    id: "load-balancer",
    label: "Least Conn",
    color: "stroke-ig-service",
    fill: "fill-ig-service/10",
    summary: "Steers traffic toward the least loaded healthy target.",
    why: "Single-instance memory is enough for this deployment and keeps selection cheap.",
    failure: "When only one replica remains, it absorbs traffic and keeps the service alive.",
    adr: ABOUT_DECISIONS[5],
  },
  {
    id: "circuit-breaker",
    label: "Circuit",
    color: "stroke-ev-error",
    fill: "fill-ev-error/10",
    summary: "Tracks failures per target and trips fast when a replica is unhealthy.",
    why: "A bad target should be isolated without dragging the full service down.",
    failure: "OPEN, HALF-OPEN, and CLOSED transitions are visible live in the observatory.",
    adr: ABOUT_DECISIONS[0],
  },
  {
    id: "observability",
    label: "Observe",
    color: "stroke-ig-observe",
    fill: "fill-ig-observe/10",
    summary: "Prometheus, Grafana, Tempo, and SSE logs all share request IDs and traces.",
    why: "The demo should explain itself as the system changes.",
    failure: "Missing telemetry turns chaos into noise, so observability is first-class.",
    adr: ABOUT_DECISIONS[7],
  },
  {
    id: "config",
    label: "Config",
    color: "stroke-text-secondary",
    fill: "fill-text-muted/10",
    summary: "Per-route auth, retry, rate limit, and targets live in declarative config.",
    why: "Operational changes should not require a code edit for every route tweak.",
    failure: "Conflicting global auth lists were removed in favor of one route-level source of truth.",
    adr: ABOUT_DECISIONS[3],
  },
] as const;

export const INTENSITY_ORDER = ["mild", "moderate", "severe", "many_keys"] as const;

export function filterIncludes(filterValue: string, eventType: string) {
  if (filterValue === "all") {
    return true;
  }

  switch (filterValue) {
    case "routing":
      return eventType === "request_routed" || eventType === "request_success";
    case "auth":
      return eventType === "auth_failed";
    case "rate-limit":
      return eventType === "rate_limited" || eventType === "redis_unavailable";
    case "retry":
      return eventType === "retry_attempt" || eventType === "upstream_5xx";
    case "circuit":
      return eventType.startsWith("circuit_") || eventType === "all_targets_exhausted";
    case "system":
      return eventType.startsWith("scenario_") || eventType === "reset_complete";
    default:
      return true;
  }
}

export function scenarioExpectedSignals(scenario?: ScenarioDetail) {
  return scenario?.expected_signals ?? [];
}
