import { useDeferredValue, useState } from "react";

import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { GrafanaEmbed } from "@/components/observability/grafana-embed";
import { useEventStream } from "@/hooks/use-event-stream";
import { buildDashboardPanelUrl, buildDashboardUrl, buildExploreUrl, buildRequestExploreUrl, buildTraceExploreUrl } from "@/lib/grafana";
import { attrString, cn, eventTone, formatTime } from "@/lib/utils";

const ranges = [
  { value: "now-15m", label: "Last 15m" },
  { value: "now-1h", label: "Last 1h" },
  { value: "now-6h", label: "Last 6h" },
];

const levelOptions = ["all", "INFO", "WARN", "ERROR"] as const;

export function ObservabilityPage() {
  const [timeRange, setTimeRange] = useState("now-15m");
  const [traceQuery, setTraceQuery] = useState('{service.name="irongate"}');
  const [levelFilter, setLevelFilter] = useState<string>("all");
  const [typeFilter, setTypeFilter] = useState<string>("all");
  const [search, setSearch] = useState("");
  const deferredSearch = useDeferredValue(search);

  const { events, traceHistory } = useEventStream("/api/events");

  const logs = events.filter((event) => {
    const levelMatches = levelFilter === "all" || event.level.toUpperCase() === levelFilter;
    const typeMatches = typeFilter === "all" || event.type === typeFilter;
    const bodyMatches = `${event.message} ${JSON.stringify(event.attrs ?? {})}`.toLowerCase().includes(deferredSearch.toLowerCase());
    return levelMatches && typeMatches && bodyMatches;
  });

  const scenarioShortcuts = Object.entries(traceHistory);
  const typeOptions = Array.from(new Set(events.map((event) => event.type))).sort();
  const traceIframeSrc = buildExploreUrl(traceQuery, timeRange, "now");

  const panels = [
    { id: 1, title: "Requests Per Second" },
    { id: 2, title: "Request Latency" },
    { id: 4, title: "Retry Activity" },
    { id: 6, title: "Rate-Limit Rejections" },
  ];

  return (
    <section className="px-4 py-6 lg:px-8">
      <div className="mx-auto max-w-[1280px] space-y-4">
        <div className="flex items-center justify-between gap-4">
          <div>
            <div className="text-xs uppercase tracking-[0.26em] text-text-muted">Page 4</div>
            <h1 className="mt-2 text-3xl font-semibold text-text-primary">Observability</h1>
          </div>
          <Button asChild variant="secondary">
            <a href={buildDashboardUrl()} target="_blank" rel="noreferrer">
              Open full Grafana →
            </a>
          </Button>
        </div>

        <div className="rounded-md border border-ig-border bg-ig-surface px-4 py-3 text-sm text-text-secondary lg:hidden">
          Best viewed on desktop. On smaller screens the embeds stay usable, but the dense panels and log stream have less breathing room.
        </div>

        <Tabs defaultValue="metrics">
          <TabsList>
            <TabsTrigger value="metrics">Metrics</TabsTrigger>
            <TabsTrigger value="traces">Traces</TabsTrigger>
            <TabsTrigger value="logs">Logs</TabsTrigger>
          </TabsList>

          <TabsContent value="metrics">
            <div className="mb-4 flex flex-wrap items-center justify-between gap-4">
              <div className="text-sm text-text-secondary">Use the same time range across all four Grafana panels.</div>
              <div className="w-full sm:w-52">
                <Select value={timeRange} onValueChange={setTimeRange}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {ranges.map((range) => (
                      <SelectItem key={range.value} value={range.value}>
                        {range.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
            <div className="grid gap-4 lg:grid-cols-2">
              {panels.map((panel) => (
                <GrafanaEmbed
                  key={panel.id}
                  title={panel.title}
                  src={buildDashboardPanelUrl(panel.id, timeRange, "now")}
                  fallbackHref={buildDashboardUrl()}
                />
              ))}
            </div>
          </TabsContent>

          <TabsContent value="traces">
            <div className="mb-4 grid gap-4 lg:grid-cols-[minmax(0,1fr)_320px]">
              <label className="panel-frame rounded-lg p-4">
                <div className="text-xs uppercase tracking-[0.22em] text-text-muted">TraceQL</div>
                <input
                  value={traceQuery}
                  onChange={(event) => setTraceQuery(event.target.value)}
                  className="mt-3 h-11 w-full rounded-md border border-ig-border bg-ig-surface-elevated px-3 text-sm text-text-primary"
                />
              </label>
              <div className="panel-frame rounded-lg p-4">
                <div className="text-xs uppercase tracking-[0.22em] text-text-muted">Scenario Trace Shortcuts</div>
                <div className="mt-3 space-y-3">
                  {scenarioShortcuts.length === 0 ? (
                    <div className="text-sm text-text-secondary">Run scenarios to build per-scenario trace shortcuts here.</div>
                  ) : (
                    scenarioShortcuts.map(([scenario, shortcuts]) => (
                      <div key={scenario}>
                        <div className="text-sm font-medium capitalize text-text-primary">{scenario.replaceAll("-", " ")}</div>
                        <div className="mt-2 flex flex-wrap gap-2">
                          {shortcuts.map((shortcut) => (
                            <a
                              key={shortcut.traceId}
                              href={buildTraceExploreUrl(shortcut.traceId)}
                              target="_blank"
                              rel="noreferrer"
                              className="rounded-sm border border-ig-border bg-ig-surface-elevated px-3 py-2 text-xs font-mono text-text-secondary"
                            >
                              {shortcut.traceId.slice(0, 12)}…
                            </a>
                          ))}
                        </div>
                      </div>
                    ))
                  )}
                </div>
              </div>
            </div>
            <GrafanaEmbed title="Tempo Explore" src={traceIframeSrc} fallbackHref={buildExploreUrl(traceQuery, timeRange, "now")} />
          </TabsContent>

          <TabsContent value="logs">
            <div className="mb-4 grid gap-4 lg:grid-cols-[220px_220px_minmax(0,1fr)]">
              <div>
                <div className="mb-2 text-xs uppercase tracking-[0.22em] text-text-muted">Level</div>
                <Select value={levelFilter} onValueChange={setLevelFilter}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {levelOptions.map((option) => (
                      <SelectItem key={option} value={option}>
                        {option}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div>
                <div className="mb-2 text-xs uppercase tracking-[0.22em] text-text-muted">Type</div>
                <Select value={typeFilter} onValueChange={setTypeFilter}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="all">all</SelectItem>
                    {typeOptions.map((option) => (
                      <SelectItem key={option} value={option}>
                        {option}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <label>
                <div className="mb-2 text-xs uppercase tracking-[0.22em] text-text-muted">Search</div>
                <input
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                  className="h-11 w-full rounded-md border border-ig-border bg-ig-surface-elevated px-3 text-sm text-text-primary"
                  placeholder="request_id, trace_id, target, message..."
                />
              </label>
            </div>

            <div className="panel-frame scrollbar-thin max-h-[44rem] overflow-y-auto rounded-lg p-4">
              <div className="space-y-3 font-mono text-sm">
                {logs.length === 0 ? (
                  <div className="rounded-md border border-dashed border-ig-border bg-ig-surface px-4 py-6 text-center font-sans text-sm text-text-secondary">
                    No log lines match the current filters.
                  </div>
                ) : (
                  logs.map((event) => {
                    const requestId = attrString(event.attrs, "request_id");
                    const tone = eventTone(event.type);
                    return (
                      <div
                        key={`${event.ts}-${event.type}-${event.message}`}
                        className={cn(
                          "rounded-md border px-4 py-3",
                          tone === "error" && "border-ev-error/30 bg-ev-error/10",
                          tone === "warning" && "border-ev-warning/30 bg-ev-warning/10",
                          tone !== "error" && tone !== "warning" && "border-ig-border bg-ig-surface",
                        )}
                      >
                        <div className="flex flex-wrap items-center gap-3 text-xs text-text-muted">
                          <span>{formatTime(event.ts)}</span>
                          <span>{event.level}</span>
                          <span>{event.type}</span>
                          {requestId && (
                            <a href={buildRequestExploreUrl(requestId)} target="_blank" rel="noreferrer" className="text-ig-gateway">
                              req={requestId}
                            </a>
                          )}
                        </div>
                        <div className="mt-2 text-text-primary">{event.message}</div>
                      </div>
                    );
                  })
                )}
              </div>
            </div>
          </TabsContent>
        </Tabs>
      </div>
    </section>
  );
}
