import {
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import { Badge } from "@/components/ui/badge";
import { cn, formatMetric, formatTime, serviceStatusTone } from "@/lib/utils";
import type { ChartSeries } from "@/types/observatory";

interface MetricPanelProps {
  children?: React.ReactNode;
  error?: string;
  idleLabel?: string;
  loading?: boolean;
  title: string;
}

function MetricPanel({ children, error, idleLabel = "No traffic", loading, title }: MetricPanelProps) {
  return (
    <div className="panel-frame rounded-lg p-4">
      <div className="mb-3 flex items-center justify-between">
        <div className="text-sm font-medium text-text-primary">{title}</div>
        {loading && <Badge>Loading</Badge>}
        {error && <Badge variant="warning">Unavailable</Badge>}
      </div>

      {loading ? (
        <div className="h-36 animate-pulse rounded-md border border-ig-border bg-ig-surface" />
      ) : error ? (
        <div className="flex h-36 items-center justify-center rounded-md border border-dashed border-ig-border bg-ig-surface px-6 text-center text-sm text-text-secondary">
          Metrics unavailable. {error}
        </div>
      ) : children ? (
        children
      ) : (
        <div className="flex h-36 items-center justify-center rounded-md border border-dashed border-ig-border bg-ig-surface text-sm text-text-secondary">
          {idleLabel}
        </div>
      )}
    </div>
  );
}

interface LineMetricPanelProps {
  colors: Record<string, string>;
  error?: string;
  label: string;
  loading?: boolean;
  series: ChartSeries[];
}

export function LineMetricPanel({ colors, error, label, loading, series }: LineMetricPanelProps) {
  const points = series[0]?.points.map((point, index) => {
    const row: Record<string, number | string> = {
      timestamp: point.timestamp,
      label: formatTime(new Date(point.timestamp).toISOString()),
    };
    for (const entry of series) {
      row[entry.name] = entry.points[index]?.value ?? 0;
    }
    return row;
  });

  return (
    <MetricPanel title={label} loading={loading} error={error}>
      {points?.length ? (
        <div className="h-36">
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={points}>
              <CartesianGrid stroke="#1F1F23" strokeDasharray="3 3" />
              <XAxis dataKey="label" tick={{ fill: "#71717A", fontSize: 10 }} minTickGap={30} />
              <YAxis tick={{ fill: "#71717A", fontSize: 10 }} width={38} />
              <Tooltip
                contentStyle={{
                  backgroundColor: "#111113",
                  borderColor: "#1F1F23",
                  color: "#FAFAFA",
                }}
                labelStyle={{ color: "#A1A1AA" }}
              />
              {series.map((entry) => (
                <Line
                  key={entry.name}
                  type="monotone"
                  dot={false}
                  strokeWidth={2}
                  dataKey={entry.name}
                  stroke={colors[entry.name] ?? "#3B82F6"}
                />
              ))}
            </LineChart>
          </ResponsiveContainer>
        </div>
      ) : null}
    </MetricPanel>
  );
}

interface StackedBarMetricPanelProps {
  error?: string;
  loading?: boolean;
  rejected: ChartSeries | null;
  total: ChartSeries | null;
  title: string;
}

export function StackedBarMetricPanel({ error, loading, rejected, title, total }: StackedBarMetricPanelProps) {
  const points = total?.points.map((point, index) => {
    const rejectedValue = rejected?.points[index]?.value ?? 0;
    return {
      label: formatTime(new Date(point.timestamp).toISOString()),
      allowed: Math.max(point.value - rejectedValue, 0),
      rejected: rejectedValue,
    };
  });

  return (
    <MetricPanel title={title} loading={loading} error={error}>
      {points?.length ? (
        <div className="h-36">
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={points}>
              <CartesianGrid stroke="#1F1F23" strokeDasharray="3 3" />
              <XAxis dataKey="label" tick={{ fill: "#71717A", fontSize: 10 }} minTickGap={30} />
              <YAxis tick={{ fill: "#71717A", fontSize: 10 }} width={38} />
              <Tooltip
                contentStyle={{
                  backgroundColor: "#111113",
                  borderColor: "#1F1F23",
                  color: "#FAFAFA",
                }}
              />
              <Bar dataKey="allowed" stackId="a" fill="#10B981" radius={[2, 2, 0, 0]} />
              <Bar dataKey="rejected" stackId="a" fill="#EF4444" radius={[2, 2, 0, 0]} />
            </BarChart>
          </ResponsiveContainer>
        </div>
      ) : null}
    </MetricPanel>
  );
}

interface CircuitTimelinePanelProps {
  error?: string;
  loading?: boolean;
  series: ChartSeries[];
}

export function CircuitTimelinePanel({ error, loading, series }: CircuitTimelinePanelProps) {
  return (
    <MetricPanel title="Circuit States" loading={loading} error={error}>
      {series.length > 0 ? (
        <div className="space-y-3">
          {series.map((entry) => (
            <div key={entry.service ?? entry.name}>
              <div className="mb-1 flex items-center justify-between text-xs text-text-muted">
                <span>{entry.service ?? entry.name}</span>
                <span>{entry.points.length} samples</span>
              </div>
              <div className="grid grid-cols-12 gap-1">
                {entry.points.slice(-12).map((point, index) => {
                  const status = point.value >= 1.5 ? "timeout" : point.value >= 0.5 ? "down" : "healthy";
                  return <div key={index} className={cn("h-5 rounded-sm", serviceStatusTone(status))} />;
                })}
              </div>
            </div>
          ))}
        </div>
      ) : null}
    </MetricPanel>
  );
}

interface CounterStripProps {
  counters: Array<{ label: string; tone?: "default" | "success" | "warning" | "error"; value: number }>;
}

export function CounterStrip({ counters }: CounterStripProps) {
  return (
    <div className="grid grid-cols-2 gap-3 xl:grid-cols-4">
      {counters.map((counter) => (
        <div key={counter.label} className="panel-frame rounded-lg px-4 py-4">
          <div className="text-[11px] uppercase tracking-[0.22em] text-text-muted">{counter.label}</div>
          <div className="mt-3 font-mono text-2xl font-semibold text-text-primary font-tabular">{formatMetric(counter.value, 0)}</div>
        </div>
      ))}
    </div>
  );
}
