import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

import type { EventAttrValue, EventRecord, PrometheusInstantVectorResult, PrometheusRangeMatrixResult } from "@/types/observatory";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function formatCompactNumber(value: number) {
  return new Intl.NumberFormat("en-US", {
    notation: "compact",
    maximumFractionDigits: value >= 100 ? 0 : 1,
  }).format(value);
}

export function formatNumber(value: number) {
  return new Intl.NumberFormat("en-US").format(Math.round(value));
}

export function formatMetric(value: number, digits = 1) {
  if (!Number.isFinite(value)) {
    return "0";
  }

  return value.toLocaleString("en-US", {
    minimumFractionDigits: 0,
    maximumFractionDigits: digits,
  });
}

export function formatDuration(durationMs?: number) {
  if (durationMs == null || !Number.isFinite(durationMs) || durationMs < 0) {
    return "--";
  }

  if (durationMs >= 1000) {
    return `${(durationMs / 1000).toFixed(1)}s`;
  }

  return `${Math.round(durationMs)}ms`;
}

export function formatTime(iso: string) {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) {
    return "--:--:--";
  }

  return date.toLocaleTimeString("en-US", {
    hour12: false,
  });
}

export function attrString(attrs: Record<string, EventAttrValue> | undefined, key: string) {
  const value = attrs?.[key];
  return typeof value === "string" && value.trim() !== "" ? value : undefined;
}

export function attrNumber(attrs: Record<string, EventAttrValue> | undefined, key: string) {
  const value = attrs?.[key];
  if (typeof value === "number") {
    return value;
  }
  if (typeof value === "string" && value.trim() !== "" && !Number.isNaN(Number(value))) {
    return Number(value);
  }
  return undefined;
}

export function instantValue(result: PrometheusInstantVectorResult[] | undefined) {
  const first = result?.[0]?.value?.[1];
  const parsed = first ? Number(first) : 0;
  return Number.isFinite(parsed) ? parsed : 0;
}

export function rangeValues(result: PrometheusRangeMatrixResult[] | undefined) {
  return (result ?? []).map((entry) => ({
    metric: entry.metric,
    points: entry.values.map(([timestamp, raw]) => ({
      timestamp: Number(timestamp) * 1000,
      value: Number(raw),
    })),
  }));
}

export function asInstantResults(result: PrometheusInstantVectorResult[] | PrometheusRangeMatrixResult[] | undefined) {
  return (result ?? []) as PrometheusInstantVectorResult[];
}

export function asRangeResults(result: PrometheusInstantVectorResult[] | PrometheusRangeMatrixResult[] | undefined) {
  return (result ?? []) as PrometheusRangeMatrixResult[];
}

export function eventTone(type: string) {
  switch (type) {
    case "request_success":
    case "circuit_closed":
      return "success";
    case "auth_failed":
    case "rate_limited":
    case "retry_attempt":
    case "circuit_half_open":
    case "redis_unavailable":
      return "warning";
    case "circuit_open":
    case "circuit_rejected":
    case "upstream_5xx":
    case "all_targets_exhausted":
      return "error";
    case "scenario_started":
    case "scenario_stopped":
    case "reset_complete":
      return "system";
    default:
      return "muted";
  }
}

export function statusTone(status: string) {
  switch (status) {
    case "running":
      return "text-ev-success";
    case "stopping":
      return "text-ev-warning";
    case "error":
      return "text-ev-error";
    default:
      return "text-text-secondary";
  }
}

export function serviceStatusTone(status: string) {
  switch (status) {
    case "healthy":
      return "bg-ev-success";
    case "timeout":
      return "bg-ev-warning";
    case "down":
      return "bg-ev-error";
    default:
      return "bg-text-muted";
  }
}

export function uniqueTraceId(event: EventRecord) {
  return attrString(event.attrs, "trace_id");
}

export function eventRequestStatus(event: EventRecord) {
  return attrNumber(event.attrs, "status");
}
