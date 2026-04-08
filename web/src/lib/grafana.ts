const DEFAULT_GRAFANA_URL = "http://127.0.0.1:3000";
const DASHBOARD_UID = "irongate-observability";
const TEMPO_UID = "tempo";

function windowSafeLocation() {
  if (typeof window === "undefined") {
    return undefined;
  }
  return window.location;
}

export function grafanaBaseUrl() {
  const location = windowSafeLocation();
  if (!location) {
    return DEFAULT_GRAFANA_URL;
  }

  return `${location.protocol}//${location.hostname}:3000`;
}

export function buildDashboardPanelUrl(panelId: number, from: string, to: string) {
  const url = new URL(`${grafanaBaseUrl()}/d-solo/${DASHBOARD_UID}/irongate-observability`);
  url.searchParams.set("orgId", "1");
  url.searchParams.set("panelId", String(panelId));
  url.searchParams.set("theme", "dark");
  url.searchParams.set("from", from);
  url.searchParams.set("to", to);
  return url.toString();
}

export function buildDashboardUrl() {
  return `${grafanaBaseUrl()}/d/${DASHBOARD_UID}/irongate-observability`;
}

function buildExploreLeft(query: string, from = "now-1h", to = "now") {
  return JSON.stringify({
    datasource: TEMPO_UID,
    queries: [
      {
        refId: "A",
        datasource: {
          type: "tempo",
          uid: TEMPO_UID,
        },
        query,
        queryType: "traceqlSearch",
      },
    ],
    range: { from, to },
  });
}

export function buildExploreUrl(query: string, from = "now-1h", to = "now") {
  const url = new URL(`${grafanaBaseUrl()}/explore`);
  url.searchParams.set("orgId", "1");
  url.searchParams.set("left", buildExploreLeft(query, from, to));
  return url.toString();
}

export function buildTraceExploreUrl(traceId: string) {
  return buildExploreUrl(`{trace:id="${traceId}"}`);
}

export function buildRequestExploreUrl(requestId: string) {
  return buildExploreUrl(`{request_id="${requestId}"}`);
}
