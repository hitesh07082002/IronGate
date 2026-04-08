import { useEffect, useRef, useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { buildTraceExploreUrl } from "@/lib/grafana";
import { attrString, eventTone, formatDuration, formatTime } from "@/lib/utils";
import type { EventConnectionStatus, EventRecord } from "@/types/observatory";

interface EventFeedProps {
  events: EventRecord[];
  isRunning: boolean;
  onRetry?: () => void;
  status: EventConnectionStatus;
}

interface EventGroup {
  key: string;
  event: EventRecord;
  items: EventRecord[];
}

function toneVariant(type: string) {
  switch (eventTone(type)) {
    case "success":
      return "success";
    case "warning":
      return "warning";
    case "error":
      return "error";
    case "system":
      return "system";
    default:
      return "default";
  }
}

export function EventFeed({ events, isRunning, onRetry, status }: EventFeedProps) {
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [paused, setPaused] = useState(false);
  const scrollRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const node = scrollRef.current;
    if (!node || paused) {
      return;
    }
    node.scrollTop = 0;
  }, [events, paused]);

  const groups: EventGroup[] = [];
  for (const event of events) {
    const key = `${event.type}:${event.message}`;
    const previous = groups[groups.length - 1];
    if (previous && previous.key === key) {
      previous.items.push(event);
      continue;
    }
    groups.push({ key, event, items: [event] });
  }

  const showError = status === "reconnecting";
  const showEmpty = events.length === 0 && !isRunning && !showError;

  return (
    <div className="panel-frame flex min-h-[22rem] flex-col rounded-lg">
      <div className="border-b border-ig-border px-4 py-4">
        <div className="flex items-center justify-between gap-3">
          <div>
            <div className="text-xs uppercase tracking-[0.24em] text-text-muted">Event Stream</div>
            <div className="mt-2 flex items-center gap-3">
              <h3 className="text-xl font-semibold text-text-primary">Live Events</h3>
              <div className="flex items-center gap-2 text-sm text-text-secondary">
                <span className="h-2.5 w-2.5 rounded-full bg-ev-success animate-pulse" />
                {status}
              </div>
            </div>
          </div>
          <Badge variant={showError ? "warning" : "default"}>{events.length} events</Badge>
        </div>
      </div>

      {showEmpty && (
        <div className="flex flex-1 items-center justify-center px-6 py-12 text-center">
          <div>
            <div className="text-lg font-medium text-text-primary">Run a scenario to see live events</div>
            <div className="mt-2 max-w-sm text-sm leading-6 text-text-secondary">
              The feed stays terminal-fast with no entrance animation. When traffic starts, request, retry, circuit, and reset events stream in live.
            </div>
          </div>
        </div>
      )}

      {showError && events.length === 0 && (
        <div className="flex flex-1 items-center justify-center px-6 py-12 text-center">
          <div>
            <div className="text-lg font-medium text-text-primary">Event connection lost</div>
            <div className="mt-2 text-sm text-text-secondary">Reconnecting to the observatory stream.</div>
            {onRetry && (
              <Button variant="secondary" className="mt-4" onClick={onRetry}>
                Retry now
              </Button>
            )}
          </div>
        </div>
      )}

      {events.length > 0 && (
        <div
          ref={scrollRef}
          className="scrollbar-thin max-h-[40rem] flex-1 overflow-y-auto px-4 py-4"
          onScroll={(event) => {
            setPaused(event.currentTarget.scrollTop > 24);
          }}
          tabIndex={0}
        >
          <div className="space-y-3">
            {groups.map((group) => {
              const traceId = attrString(group.event.attrs, "trace_id");
              const requestId = attrString(group.event.attrs, "request_id");
              const route = attrString(group.event.attrs, "route");
              const duration = group.event.attrs ? Number(group.event.attrs["duration_ms"]) : undefined;
              const count = group.items.length;
              const groupId = `${group.key}:${group.event.ts}`;

              return (
                <div key={`${group.key}-${group.event.ts}`} className="rounded-md border border-ig-border bg-ig-surface px-4 py-3">
                  <div className="flex items-start justify-between gap-4">
                    <div className="space-y-2">
                      <div className="flex flex-wrap items-center gap-2">
                        <Badge variant={toneVariant(group.event.type)}>{group.event.type.replaceAll("_", " ")}</Badge>
                        <span className="font-mono text-xs text-text-muted">{formatTime(group.event.ts)}</span>
                        {requestId && <span className="font-mono text-xs text-text-muted">req={requestId}</span>}
                        {route && <span className="font-mono text-xs text-text-muted">{route}</span>}
                      </div>
                      <div className="font-mono text-sm leading-6 text-text-primary">
                        {group.event.message}
                        {typeof duration === "number" && (
                          <span className="ml-2 text-text-muted">({formatDuration(duration)})</span>
                        )}
                      </div>
                    </div>

                    <div className="flex items-center gap-3">
                      {count > 1 && (
                        <button
                          type="button"
                          className="text-xs text-text-secondary transition hover:text-text-primary"
                          onClick={() =>
                            setExpanded((current) => ({
                              ...current,
                              [groupId]: !current[groupId],
                            }))
                          }
                        >
                          {count} events {expanded[groupId] ? "↑" : "↓"}
                        </button>
                      )}
                      {traceId && (
                        <a
                          href={buildTraceExploreUrl(traceId)}
                          target="_blank"
                          rel="noreferrer"
                          className="text-sm font-medium text-ig-gateway transition hover:text-blue-400"
                        >
                          View Trace →
                        </a>
                      )}
                    </div>
                  </div>

                  {count > 1 && expanded[groupId] && (
                    <div className="mt-3 space-y-2 border-t border-ig-border pt-3">
                      {group.items.slice(1).map((item, index) => (
                        <div key={`${item.ts}-${index}`} className="font-mono text-xs text-text-muted">
                          {formatTime(item.ts)} {item.message}
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        </div>
      )}

      {paused && events.length > 0 && (
        <div className="border-t border-ig-border px-4 py-3">
          <Button
            variant="secondary"
            size="sm"
            onClick={() => {
              setPaused(false);
              scrollRef.current?.scrollTo({ top: 0, behavior: "smooth" });
            }}
          >
            Jump to latest
          </Button>
        </div>
      )}
    </div>
  );
}
