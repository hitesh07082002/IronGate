export type ScenarioStatus = "idle" | "running" | "stopping" | "error";
export type ServiceStatus = "healthy" | "down" | "timeout" | "unknown";
export type EventConnectionStatus = "connecting" | "open" | "reconnecting" | "closed";
export type PrometheusMode = "instant" | "range";

export interface IntensityOption {
  rps: number;
}

export interface ScenarioSummary {
  name: string;
  display_name: string;
  category: string;
  intensity_options: Record<string, IntensityOption>;
  duration_options: number[];
}

export interface ChaosStep {
  at_seconds: number;
  action: string;
  target?: string;
  params?: Record<string, unknown>;
}

export interface ExpectedSignal {
  panel?: string;
  signal?: string;
  event_feed?: string[];
}

export interface ScenarioDetail extends ScenarioSummary {
  description: string;
  what_you_learn: string;
  what_to_watch: string;
  chaos_sequence: ChaosStep[];
  expected_signals?: ExpectedSignal[];
}

export interface EventAttrObject {
  [key: string]: EventAttrValue;
}

export type EventAttrValue = string | number | boolean | null | EventAttrValue[] | EventAttrObject;

export interface EventRecord {
  ts: string;
  level: string;
  type: string;
  message: string;
  attrs?: Record<string, EventAttrValue>;
}

export interface TraceShortcut {
  traceId: string;
  requestId?: string;
  method?: string;
  path?: string;
  status?: number;
  durationMs?: number;
  scenario?: string;
  timestamp: string;
}

export interface SystemServiceHealth {
  name: string;
  status: ServiceStatus;
  checked_at?: string;
  details?: string;
}

export interface HealthResponse {
  status: string;
  spec_version: string;
  jwt_valid: boolean;
  toxiproxy_ready: boolean;
  services?: SystemServiceHealth[];
}

export interface ResetResponse {
  status: "clean" | "partial";
  services_healthy?: boolean;
  failed_step?: string;
  details?: string;
}

export interface PrometheusInstantVectorResult {
  metric: Record<string, string>;
  value: [number, string];
}

export interface PrometheusRangeMatrixResult {
  metric: Record<string, string>;
  values: Array<[number, string]>;
}

export interface PrometheusResponse<T> {
  status: string;
  data: {
    resultType: string;
    result: T[];
  };
}

export interface LandingDashboardResponse {
  in_flight: PrometheusResponse<PrometheusInstantVectorResult>;
  total_rps: PrometheusResponse<PrometheusInstantVectorResult>;
  error_rps: PrometheusResponse<PrometheusInstantVectorResult>;
  requests_served: PrometheusResponse<PrometheusInstantVectorResult>;
  circuit_events: PrometheusResponse<PrometheusInstantVectorResult>;
  rate_limited: PrometheusResponse<PrometheusInstantVectorResult>;
}

export interface ChaosDashboardResponse {
  request_rate: PrometheusResponse<PrometheusRangeMatrixResult>;
  error_rate: PrometheusResponse<PrometheusRangeMatrixResult>;
  latency_p50: PrometheusResponse<PrometheusRangeMatrixResult>;
  latency_p95: PrometheusResponse<PrometheusRangeMatrixResult>;
  latency_p99: PrometheusResponse<PrometheusRangeMatrixResult>;
  circuit_state: PrometheusResponse<PrometheusRangeMatrixResult>;
  total_rate: PrometheusResponse<PrometheusRangeMatrixResult>;
  rejected_rate: PrometheusResponse<PrometheusRangeMatrixResult>;
  success_count: PrometheusResponse<PrometheusInstantVectorResult>;
  error_count: PrometheusResponse<PrometheusInstantVectorResult>;
  retry_count: PrometheusResponse<PrometheusInstantVectorResult>;
  rate_limited_count: PrometheusResponse<PrometheusInstantVectorResult>;
}

export interface TimeSeriesPoint {
  timestamp: number;
  value: number;
}

export interface ChartSeries {
  name: string;
  service?: string;
  points: TimeSeriesPoint[];
}
