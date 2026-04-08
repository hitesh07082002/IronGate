import { DEMO_TOKEN } from "@/lib/constants";
import type {
  ChaosDashboardResponse,
  HealthResponse,
  LandingDashboardResponse,
  PrometheusInstantVectorResult,
  PrometheusMode,
  PrometheusRangeMatrixResult,
  PrometheusResponse,
  ResetResponse,
  ScenarioDetail,
  ScenarioStatus,
  ScenarioSummary,
} from "@/types/observatory";

async function fetchJSON<T>(input: string, init?: RequestInit) {
  const response = await fetch(input, init);
  if (!response.ok) {
    const body = await response.text();
    let payload: unknown;
    try {
      payload = body ? JSON.parse(body) : undefined;
    } catch {
      payload = undefined;
    }

    const record = payload && typeof payload === "object" ? (payload as Record<string, unknown>) : undefined;
    const errorField = record?.error;
    const message =
      (typeof errorField === "string" ? errorField : undefined) ||
      (errorField && typeof errorField === "object" && typeof (errorField as Record<string, unknown>).message === "string"
        ? ((errorField as Record<string, unknown>).message as string)
        : undefined) ||
      (typeof record?.message === "string" ? record.message : undefined) ||
      body.trim() ||
      `Request failed with ${response.status}`;
    const requestId =
      (typeof record?.request_id === "string" ? record.request_id : undefined) ||
      (typeof record?.requestId === "string" ? record.requestId : undefined) ||
      (typeof record?.correlation_id === "string" ? record.correlation_id : undefined) ||
      (typeof record?.correlationId === "string" ? record.correlationId : undefined);

    throw new Error(requestId ? `${message} (${requestId})` : message);
  }

  return (await response.json()) as T;
}

function mutationHeaders() {
  return {
    "Content-Type": "application/json",
    Authorization: `Bearer ${DEMO_TOKEN}`,
  };
}

export function fetchHealth() {
  return fetchJSON<HealthResponse>("/api/health");
}

export function fetchScenarios() {
  return fetchJSON<ScenarioSummary[]>("/api/scenarios");
}

export function fetchScenario(name: string) {
  return fetchJSON<ScenarioDetail>(`/api/scenarios/${name}`);
}

export async function fetchScenarioStatuses(names: string[]) {
  if (names.length === 0) {
    return {};
  }

  return fetchJSON<Record<string, ScenarioStatus>>("/api/scenarios/statuses");
}

export function runScenario(name: string, intensity: string, duration: number) {
  return fetchJSON<{ status: string }>(`/api/scenarios/${name}/run`, {
    method: "POST",
    headers: mutationHeaders(),
    body: JSON.stringify({ intensity, duration }),
  });
}

export function stopScenario(name: string) {
  return fetchJSON<{ status: ScenarioStatus }>(`/api/scenarios/${name}/stop`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${DEMO_TOKEN}`,
    },
  });
}

export function resetSystem() {
  return fetchJSON<ResetResponse>("/api/reset", {
    method: "POST",
    headers: {
      Authorization: `Bearer ${DEMO_TOKEN}`,
    },
  });
}

export function fetchLandingDashboard() {
  return fetchJSON<LandingDashboardResponse>("/api/dashboard/landing");
}

export function fetchChaosDashboard() {
  return fetchJSON<ChaosDashboardResponse>("/api/dashboard/chaos");
}

export async function fetchPrometheus(
  mode: PrometheusMode,
  query: string,
  stepSeconds: number,
  rangeSeconds: number,
) {
  const now = Math.floor(Date.now() / 1000);
  const endpoint = mode === "range" ? "/api/metrics/query_range" : "/api/metrics/query";
  const url = new URL(endpoint, window.location.origin);
  url.searchParams.set("query", query);
  if (mode === "range") {
    url.searchParams.set("start", String(now - rangeSeconds));
    url.searchParams.set("end", String(now));
    url.searchParams.set("step", `${stepSeconds}s`);
    return fetchJSON<PrometheusResponse<PrometheusRangeMatrixResult>>(url.pathname + url.search);
  }

  return fetchJSON<PrometheusResponse<PrometheusInstantVectorResult>>(url.pathname + url.search);
}
