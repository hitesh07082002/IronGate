import { useEffect, useRef, useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { EventFeed } from "@/components/observatory/event-feed";
import {
  CircuitTimelinePanel,
  CounterStrip,
  LineMetricPanel,
  StackedBarMetricPanel,
} from "@/components/observatory/metric-panels";
import { ScenarioSidebar } from "@/components/observatory/scenario-sidebar";
import { useChaosDashboard } from "@/hooks/use-dashboard";
import { useEventStream } from "@/hooks/use-event-stream";
import { useScenario } from "@/hooks/use-scenario";
import { useSystemStatus } from "@/hooks/use-system-status";
import { EVENT_FILTERS, filterIncludes } from "@/lib/constants";
import { buildTraceExploreUrl } from "@/lib/grafana";
import { asInstantResults, asRangeResults, attrNumber, attrString, cn, formatDuration, instantValue, rangeValues } from "@/lib/utils";

export function ChaosPage() {
  const [filterValue, setFilterValue] = useState("all");
  const [metricsVisible, setMetricsVisible] = useState(false);
  const [activeStartAt, setActiveStartAt] = useState<number | null>(null);
  const [elapsed, setElapsed] = useState(0);

  const runButtonRef = useRef<HTMLButtonElement | null>(null);

  const scenarioState = useScenario();
  const systemStatus = useSystemStatus();
  const dashboard = useChaosDashboard();
  const { eventCount, events, status, traceHistory } = useEventStream("/api/events");

  useEffect(() => {
    if (scenarioState.runningScenarioName && !activeStartAt) {
      setActiveStartAt(Date.now());
    }
    if (!scenarioState.runningScenarioName) {
      setActiveStartAt(null);
      setElapsed(0);
    }
  }, [activeStartAt, scenarioState.runningScenarioName]);

  useEffect(() => {
    if (!activeStartAt) {
      return;
    }
    const timer = window.setInterval(() => {
      setElapsed((Date.now() - activeStartAt) / 1000);
    }, 1000);
    return () => window.clearInterval(timer);
  }, [activeStartAt]);

  const filteredEvents = events.filter((event) => filterIncludes(filterValue, event.type));
  const traceEvents = events.filter((event) => attrString(event.attrs, "trace_id"));
  const recentTraces = traceEvents.slice(0, 3).map((event) => ({
    traceId: attrString(event.attrs, "trace_id") ?? "",
    method: attrString(event.attrs, "method") ?? "REQ",
    path: attrString(event.attrs, "route") ?? attrString(event.attrs, "path") ?? "/",
    status: attrNumber(event.attrs, "status"),
    durationMs: attrNumber(event.attrs, "duration_ms"),
  }));

  const requestSeries = rangeValues(asRangeResults(dashboard.data?.request_rate.data.result)).map((entry) => ({
    name: "total",
    points: entry.points,
  }));
  const errorSeries = rangeValues(asRangeResults(dashboard.data?.error_rate.data.result)).map((entry) => ({
    name: "errors",
    points: entry.points,
  }));
  const latencySeries = [
    ...rangeValues(asRangeResults(dashboard.data?.latency_p50.data.result)).map((entry) => ({ name: "p50", points: entry.points })),
    ...rangeValues(asRangeResults(dashboard.data?.latency_p95.data.result)).map((entry) => ({ name: "p95", points: entry.points })),
    ...rangeValues(asRangeResults(dashboard.data?.latency_p99.data.result)).map((entry) => ({ name: "p99", points: entry.points })),
  ];
  const circuitSeries = rangeValues(asRangeResults(dashboard.data?.circuit_state.data.result)).map((entry) => ({
    name: entry.metric.service ?? "service",
    service: entry.metric.service ?? "service",
    points: entry.points,
  }));
  const totalRateResults = rangeValues(asRangeResults(dashboard.data?.total_rate.data.result));
  const rejectedRateResults = rangeValues(asRangeResults(dashboard.data?.rejected_rate.data.result));
  const totalRateSeries = totalRateResults[0]
    ? { name: "total", points: totalRateResults[0].points }
    : null;
  const rejectedRateSeries = rejectedRateResults[0]
    ? { name: "rejected", points: rejectedRateResults[0].points }
    : null;

  const counterValues = [
    {
      label: "Success",
      value: Math.max(
        instantValue(asInstantResults(dashboard.data?.success_count.data.result)) -
          instantValue(asInstantResults(dashboard.data?.error_count.data.result)),
        0,
      ),
    },
    {
      label: "Error",
      value: instantValue(asInstantResults(dashboard.data?.error_count.data.result)),
    },
    {
      label: "Retry",
      value: instantValue(asInstantResults(dashboard.data?.retry_count.data.result)),
    },
    {
      label: "Rate-Limited",
      value: instantValue(asInstantResults(dashboard.data?.rate_limited_count.data.result)),
    },
  ];

  return (
    <section className="px-4 py-6 lg:px-8">
      <div className="mx-auto max-w-[1280px] space-y-4">
        <div className="flex items-center justify-between gap-4">
          <div>
            <div className="text-xs uppercase tracking-[0.26em] text-text-muted">Page 3</div>
            <h1 className="mt-2 text-3xl font-semibold text-text-primary">Chaos Observatory</h1>
          </div>
          <div className="flex items-center gap-3">
            <Badge variant={status === "open" ? "success" : "warning"}>{status}</Badge>
            <Button variant="secondary" className="lg:hidden" onClick={() => setMetricsVisible((current) => !current)}>
              {metricsVisible ? "Hide Metrics" : "Show Metrics"}
            </Button>
          </div>
        </div>

        <div className="rounded-md border border-ig-border bg-ig-surface px-4 py-3 text-sm text-text-secondary lg:hidden">
          Best viewed on desktop. Mobile keeps the event stream first and tucks charts behind a toggle.
        </div>

        <div className="grid gap-4 xl:grid-cols-[280px_minmax(0,1fr)_320px]">
          <aside aria-label="Scenario navigation">
            <ScenarioSidebar
              activeElapsedSeconds={elapsed}
              detail={scenarioState.scenario}
              loading={scenarioState.scenariosLoading || scenarioState.scenarioLoading}
              onDurationChange={scenarioState.setSelectedDuration}
              onIntensityChange={scenarioState.setSelectedIntensity}
              onReset={scenarioState.resetSystem}
              onRun={() =>
                scenarioState.runScenario({
                  name: scenarioState.selectedName ?? "",
                  intensity: scenarioState.selectedIntensity,
                  duration: scenarioState.selectedDuration,
                })
              }
              onSelect={scenarioState.setSelectedName}
              onStop={() => scenarioState.stopScenario(scenarioState.runningScenarioName ?? scenarioState.selectedName ?? "")}
              resetDisabled={scenarioState.resetting || scenarioState.running}
              runningName={scenarioState.runningScenarioName}
              runningStatus={scenarioState.runningScenarioStatus}
              runButtonRef={runButtonRef}
              scenarioStatuses={scenarioState.scenarioStatuses}
              scenarios={scenarioState.scenarios}
              selectedDuration={scenarioState.selectedDuration}
              selectedIntensity={scenarioState.selectedIntensity}
              selectedName={scenarioState.selectedName}
              services={systemStatus.data?.services}
              servicesLoading={systemStatus.isLoading}
              statusesLoading={scenarioState.statusesLoading}
            />
          </aside>

          <main className="space-y-4">
            <div className="panel-frame rounded-lg px-4 py-4">
              <div className="flex flex-wrap items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                  <h2 className="text-xl font-semibold text-text-primary">Live Events</h2>
                  <div className="flex items-center gap-2 text-sm text-text-secondary">
                    <span className="h-2.5 w-2.5 rounded-full bg-ev-success animate-pulse" />
                    LIVE
                  </div>
                </div>
                <Badge>{eventCount} events</Badge>
              </div>
              <div className="mt-4 flex flex-wrap gap-2">
                {EVENT_FILTERS.map((filter) => (
                  <Button
                    key={filter.value}
                    size="sm"
                    variant={filterValue === filter.value ? "default" : "secondary"}
                    onClick={() => setFilterValue(filter.value)}
                  >
                    {filter.label}
                  </Button>
                ))}
              </div>
            </div>

            <CounterStrip counters={counterValues} />

            <EventFeed events={filteredEvents} isRunning={Boolean(scenarioState.runningScenarioName)} status={status} />
          </main>

          <aside aria-label="Metrics panel" className={cn("space-y-4", !metricsVisible && "hidden lg:block")}>
            <LineMetricPanel
              label="Request Rate"
              loading={dashboard.isLoading}
              error={dashboard.error instanceof Error ? dashboard.error.message : undefined}
              series={[...requestSeries, ...errorSeries]}
              colors={{ total: "#3B82F6", errors: "#EF4444" }}
            />
            <LineMetricPanel
              label="Latency"
              loading={dashboard.isLoading}
              error={dashboard.error instanceof Error ? dashboard.error.message : undefined}
              series={latencySeries}
              colors={{ p50: "#10B981", p95: "#F59E0B", p99: "#EF4444" }}
            />
            <CircuitTimelinePanel
              loading={dashboard.isLoading}
              error={dashboard.error instanceof Error ? dashboard.error.message : undefined}
              series={circuitSeries}
            />
            <StackedBarMetricPanel
              title="Rate Limit Activity"
              loading={dashboard.isLoading}
              error={dashboard.error instanceof Error ? dashboard.error.message : undefined}
              total={totalRateSeries}
              rejected={rejectedRateSeries}
            />
          </aside>
        </div>

        <div className="panel-frame rounded-lg px-4 py-4">
          <div className="flex items-center justify-between gap-3">
            <div>
              <div className="text-xs uppercase tracking-[0.22em] text-text-muted">Bottom Trace Bar</div>
              <div className="mt-1 text-sm text-text-secondary">Three freshest trace IDs seen on the live stream.</div>
            </div>
            <Badge variant="system">{Object.keys(traceHistory).length} scenario buckets</Badge>
          </div>
          <div className="mt-4 grid gap-3 md:grid-cols-3">
            {recentTraces.length === 0 ? (
              <div className="col-span-full rounded-md border border-dashed border-ig-border bg-ig-surface px-4 py-6 text-center text-sm text-text-secondary">
                Run a scenario to see traces here.
              </div>
            ) : (
              recentTraces.map((trace) => (
                <div key={trace.traceId} className="rounded-md border border-ig-border bg-ig-surface px-4 py-4">
                  <div className="font-mono text-xs text-text-muted">{trace.traceId.slice(0, 16)}…</div>
                  <div className="mt-2 text-sm font-medium text-text-primary">
                    {trace.method} {trace.path}
                  </div>
                  <div className="mt-2 flex items-center justify-between text-sm text-text-secondary">
                    <span>{trace.status ?? "—"}</span>
                    <span>{formatDuration(trace.durationMs)}</span>
                  </div>
                  <a href={buildTraceExploreUrl(trace.traceId)} target="_blank" rel="noreferrer" className="mt-3 inline-block text-sm font-medium text-ig-gateway">
                    Open in Grafana →
                  </a>
                </div>
              ))
            )}
          </div>
        </div>
      </div>
    </section>
  );
}
