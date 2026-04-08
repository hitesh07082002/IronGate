import { useEffect, useRef, useState } from "react";

import { attrNumber, attrString } from "@/lib/utils";
import type { EventConnectionStatus, EventRecord, TraceShortcut } from "@/types/observatory";

const TRACE_HISTORY_KEY = "irongate.trace-history";

function readStoredTraceHistory() {
  if (typeof window === "undefined") {
    return {} as Record<string, TraceShortcut[]>;
  }

  try {
    const raw = window.localStorage.getItem(TRACE_HISTORY_KEY);
    if (!raw) {
      return {} as Record<string, TraceShortcut[]>;
    }

    return JSON.parse(raw) as Record<string, TraceShortcut[]>;
  } catch {
    return {} as Record<string, TraceShortcut[]>;
  }
}

function pruneEvents(events: EventRecord[]) {
  const cutoff = Date.now() - 5 * 60 * 1000;
  return events.filter((event) => {
    const ts = new Date(event.ts).getTime();
    return Number.isNaN(ts) ? true : ts >= cutoff;
  });
}

function createTraceShortcut(event: EventRecord, activeScenario: string | null): TraceShortcut | null {
  const traceId = attrString(event.attrs, "trace_id");
  if (!traceId) {
    return null;
  }

  return {
    traceId,
    requestId: attrString(event.attrs, "request_id"),
    method: attrString(event.attrs, "method"),
    path: attrString(event.attrs, "route") || attrString(event.attrs, "path"),
    status: attrNumber(event.attrs, "status"),
    durationMs: attrNumber(event.attrs, "duration_ms"),
    scenario: activeScenario ?? undefined,
    timestamp: event.ts,
  };
}

export function useEventStream(url: string) {
  const [events, setEvents] = useState<EventRecord[]>([]);
  const [status, setStatus] = useState<EventConnectionStatus>("connecting");
  const [traceHistory, setTraceHistory] = useState<Record<string, TraceShortcut[]>>(readStoredTraceHistory);
  const activeScenarioRef = useRef<string | null>(null);

  useEffect(() => {
    let source: EventSource | null = null;
    let reconnectTimer: number | null = null;
    let retryDelay = 1000;
    let isDisposed = false;

    const connect = () => {
      if (isDisposed) {
        return;
      }

      setStatus((current) => (current === "open" ? "reconnecting" : "connecting"));
      source = new EventSource(url);

      source.onopen = () => {
        retryDelay = 1000;
        setStatus("open");
      };

      source.onmessage = (message) => {
        try {
          const event = JSON.parse(message.data) as EventRecord;
          setEvents((current) => pruneEvents([event, ...current]));

          if (event.type === "scenario_started") {
            activeScenarioRef.current = attrString(event.attrs, "scenario") ?? null;
          }
          if (event.type === "scenario_stopped") {
            activeScenarioRef.current = null;
          }

          const shortcut = createTraceShortcut(event, activeScenarioRef.current);
          if (!shortcut?.scenario) {
            return;
          }

          setTraceHistory((current) => {
            const existing = current[shortcut.scenario ?? ""] ?? [];
            const next = [shortcut, ...existing.filter((entry) => entry.traceId !== shortcut.traceId)].slice(0, 5);
            return {
              ...current,
              [shortcut.scenario ?? ""]: next,
            };
          });
        } catch {
          // Ignore malformed event payloads and keep the stream alive.
        }
      };

      source.onerror = () => {
        source?.close();
        setStatus("reconnecting");
        reconnectTimer = window.setTimeout(connect, retryDelay);
        retryDelay = Math.min(retryDelay * 2, 10_000);
      };
    };

    connect();

    return () => {
      isDisposed = true;
      setStatus("closed");
      if (reconnectTimer !== null) {
        window.clearTimeout(reconnectTimer);
      }
      source?.close();
    };
  }, [url]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    try {
      window.localStorage.setItem(TRACE_HISTORY_KEY, JSON.stringify(traceHistory));
    } catch {
      // Ignore quota and restricted-storage failures so the stream stays live.
    }
  }, [traceHistory]);

  return {
    events,
    status,
    traceHistory,
    eventCount: events.length,
  };
}
