import { useEffect } from "react";

import { Badge } from "@/components/ui/badge";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { SYSTEM_SERVICES, scenarioExpectedSignals } from "@/lib/constants";
import { cn, serviceStatusTone, statusTone } from "@/lib/utils";
import type { ScenarioDetail, ScenarioSummary, ScenarioStatus, SystemServiceHealth } from "@/types/observatory";

interface ScenarioSidebarProps {
  activeElapsedSeconds: number;
  detail?: ScenarioDetail;
  loading: boolean;
  onDurationChange: (duration: number) => void;
  onIntensityChange: (intensity: string) => void;
  onReset: () => Promise<unknown>;
  onRun: () => Promise<unknown>;
  onSelect: (name: string) => void;
  onStop: () => Promise<unknown>;
  resetDisabled: boolean;
  runningName?: string | null;
  runningStatus?: ScenarioStatus;
  runButtonRef: React.RefObject<HTMLButtonElement>;
  scenarioStatuses: Record<string, ScenarioStatus>;
  scenarios: ScenarioSummary[];
  selectedDuration: number;
  selectedIntensity: string;
  selectedName?: string | null;
  services?: SystemServiceHealth[];
  servicesLoading?: boolean;
  statusesLoading?: boolean;
}

export function ScenarioSidebar({
  activeElapsedSeconds,
  detail,
  loading,
  onDurationChange,
  onIntensityChange,
  onReset,
  onRun,
  onSelect,
  onStop,
  resetDisabled,
  runningName,
  runningStatus,
  runButtonRef,
  scenarioStatuses,
  scenarios,
  selectedDuration,
  selectedIntensity,
  selectedName,
  services,
  servicesLoading,
}: ScenarioSidebarProps) {
  useEffect(() => {
    if (!loading && selectedName) {
      runButtonRef.current?.focus();
    }
  }, [loading, runButtonRef, selectedName]);

  const activeSteps = detail?.chaos_sequence ?? [];
  const serviceMap = Object.fromEntries((services ?? []).map((service) => [service.name, service]));

  return (
    <div className="flex h-full flex-col gap-4">
      <div className="panel-frame rounded-lg p-4">
        <div className="flex items-center justify-between">
          <div>
            <div className="text-xs uppercase tracking-[0.24em] text-text-muted">Scenario Control</div>
            <h2 className="mt-2 text-xl font-semibold text-text-primary">Chaos Library</h2>
          </div>
          {runningName ? <Badge variant="system">Live</Badge> : <Badge>Idle</Badge>}
        </div>

        {runningName ? (
          <div className="mt-4 rounded-md border border-ig-gateway/40 bg-ig-gateway/10 p-4">
            <div className="flex items-start justify-between gap-3">
              <div>
                <div className="text-xs uppercase tracking-[0.22em] text-ig-gateway">Active Scenario</div>
                <div className="mt-2 text-lg font-semibold text-text-primary">
                  {detail?.display_name ?? scenarios.find((scenario) => scenario.name === runningName)?.display_name ?? runningName}
                </div>
              </div>
              <div className={cn("text-sm font-medium uppercase", statusTone(runningStatus ?? "idle"))}>
                {runningStatus ?? "running"}
              </div>
            </div>
            <div className="mt-3 text-sm text-text-secondary">Elapsed: {Math.floor(activeElapsedSeconds)}s</div>
            <div className="mt-4 flex flex-col gap-3">
              {activeSteps.length === 0 ? (
                <div className="rounded-sm border border-ig-border bg-ig-surface px-3 py-2 text-sm text-text-secondary">
                  No timed chaos steps for this scenario. Watch the auth, retry, rate-limit, and trace signals while live traffic is running.
                </div>
              ) : (
                activeSteps.map((step, index) => {
                  const complete = activeElapsedSeconds >= step.at_seconds;
                  return (
                    <div key={`${step.action}-${index}`} className="flex items-center gap-3 text-sm">
                      <div
                        className={cn(
                          "flex h-7 w-7 items-center justify-center rounded-full border text-xs font-semibold",
                          complete
                            ? "border-ev-success/50 bg-ev-success/10 text-ev-success"
                            : "border-ig-border bg-ig-surface text-text-muted",
                        )}
                      >
                        {complete ? "✓" : index + 1}
                      </div>
                      <div className="text-text-secondary">
                        <span className="font-medium text-text-primary">{step.action.replaceAll("_", " ")}</span> at {step.at_seconds}s
                      </div>
                    </div>
                  );
                })
              )}
            </div>
          </div>
        ) : (
          <div className="mt-4 flex flex-col gap-3">
            {loading ? (
              Array.from({ length: 4 }).map((_, index) => (
                <div key={index} className="animate-pulse rounded-md border border-ig-border bg-ig-surface p-4">
                  <div className="h-4 w-32 rounded bg-zinc-800" />
                  <div className="mt-3 h-3 w-40 rounded bg-zinc-900" />
                </div>
              ))
            ) : (
              scenarios.map((scenario) => {
                const selected = scenario.name === selectedName;
                const status = scenarioStatuses[scenario.name] ?? "idle";

                return (
                  <button
                    key={scenario.name}
                    type="button"
                    onClick={() => onSelect(scenario.name)}
                    className={cn(
                      "rounded-md border p-4 text-left transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ig-gateway",
                      selected
                        ? "border-ig-gateway bg-ig-gateway/10"
                        : "border-ig-border bg-ig-surface hover:border-zinc-700",
                    )}
                  >
                    <div className="flex items-center justify-between gap-3">
                      <div>
                        <div className="text-sm font-semibold text-text-primary">{scenario.display_name}</div>
                        <div className="mt-1 text-xs uppercase tracking-[0.18em] text-text-muted">
                          {scenario.category.replaceAll("_", " ")}
                        </div>
                      </div>
                      <div className={cn("text-xs font-medium uppercase", statusTone(status))}>{status}</div>
                    </div>

                    {selected && detail && (
                      <div className="mt-4 space-y-4">
                        <div className="rounded-sm border border-ig-border bg-ig-surface-elevated p-3">
                          <div className="text-xs uppercase tracking-[0.2em] text-text-muted">What to watch</div>
                          <div className="mt-2 text-sm leading-6 text-text-secondary">{detail.what_to_watch}</div>
                        </div>

                        <div>
                          <div className="text-xs uppercase tracking-[0.2em] text-text-muted">Intensity</div>
                          <div className="mt-2 flex flex-wrap gap-2">
                            {Object.keys(detail.intensity_options).map((intensity) => (
                              <Button
                                key={intensity}
                                variant={selectedIntensity === intensity ? "default" : "secondary"}
                                size="sm"
                                type="button"
                                onClick={(event) => {
                                  event.stopPropagation();
                                  onIntensityChange(intensity);
                                }}
                              >
                                {intensity.replaceAll("_", " ")}
                              </Button>
                            ))}
                          </div>
                        </div>

                        <div>
                          <div className="text-xs uppercase tracking-[0.2em] text-text-muted">Duration</div>
                          <div className="mt-2 flex flex-wrap gap-2">
                            {detail.duration_options.map((duration) => (
                              <Button
                                key={duration}
                                variant={selectedDuration === duration ? "default" : "secondary"}
                                size="sm"
                                type="button"
                                onClick={(event) => {
                                  event.stopPropagation();
                                  onDurationChange(duration);
                                }}
                              >
                                {duration}s
                              </Button>
                            ))}
                          </div>
                        </div>

                        <div>
                          <div className="text-xs uppercase tracking-[0.2em] text-text-muted">Expected signals</div>
                          <div className="mt-2 flex flex-col gap-2">
                            {scenarioExpectedSignals(detail).map((signal, index) => (
                              <div key={index} className="rounded-sm border border-ig-border bg-ig-surface-elevated px-3 py-2 text-sm text-text-secondary">
                                <span className="font-medium text-text-primary">{signal.panel ?? "Event Feed"}:</span>{" "}
                                {signal.signal ?? signal.event_feed?.join(", ")}
                              </div>
                            ))}
                          </div>
                        </div>
                      </div>
                    )}
                  </button>
                );
              })
            )}
          </div>
        )}
      </div>

      <div className="panel-frame rounded-lg p-4">
        <div className="text-xs uppercase tracking-[0.22em] text-text-muted">System Status</div>
        <div className="mt-4 grid grid-cols-2 gap-3">
          {SYSTEM_SERVICES.map((name) => {
            const service = serviceMap[name];
            return (
              <div key={name} className="flex items-center gap-3 rounded-sm border border-ig-border bg-ig-surface px-3 py-3 text-sm">
                <div
                  className={cn(
                    "h-3 w-3 rounded-full",
                    servicesLoading ? "animate-pulse bg-text-muted" : serviceStatusTone(service?.status ?? "unknown"),
                  )}
                />
                <div className="truncate text-text-secondary">{name}</div>
              </div>
            );
          })}
        </div>
        <div className="mt-3 text-xs text-text-muted">
          {servicesLoading ? "Checking live service health..." : "Green healthy, yellow timeout, red unreachable."}
        </div>
      </div>

      <div className="sticky bottom-4 mt-auto panel-frame rounded-lg p-4">
        <div className="flex flex-col gap-3">
          <Button
            ref={runButtonRef}
            type="button"
            onClick={() => void onRun()}
            disabled={resetDisabled || !selectedName || Boolean(runningName)}
            className="w-full"
          >
            Launch Scenario →
          </Button>
          <Button type="button" variant="secondary" onClick={() => void onStop()} disabled={!runningName} className="w-full">
            Stop Active Run
          </Button>
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button type="button" variant="ghost" disabled={resetDisabled} className="w-full">
                Reset All
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>Reset the observatory?</AlertDialogTitle>
                <AlertDialogDescription>
                  This stops k6 traffic, clears toxics, flushes circuit breakers, and waits for the demo system to return clean.
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel className="rounded-md border border-ig-border px-4 py-2 text-sm text-text-secondary">
                  Cancel
                </AlertDialogCancel>
                <AlertDialogAction
                  className="rounded-md bg-ev-error px-4 py-2 text-sm font-medium text-text-primary"
                  onClick={() => void onReset()}
                >
                  Reset System
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </div>
      </div>
    </div>
  );
}
