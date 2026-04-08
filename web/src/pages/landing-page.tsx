import { Link } from "react-router-dom";

import { Button } from "@/components/ui/button";
import { PipelineDiagram } from "@/components/pipeline/pipeline-diagram";
import { useLandingDashboard } from "@/hooks/use-dashboard";
import { asInstantResults, instantValue, formatNumber } from "@/lib/utils";

export function LandingPage() {
  const landingDashboard = useLandingDashboard();

  const inFlight = instantValue(asInstantResults(landingDashboard.data?.in_flight.data.result));
  const totalRps = instantValue(asInstantResults(landingDashboard.data?.total_rps.data.result));
  const errorRps = instantValue(asInstantResults(landingDashboard.data?.error_rps.data.result));
  const errorRatio = totalRps > 0 ? errorRps / totalRps : 0;

  const stats = [
    {
      label: "Requests Served",
      value: instantValue(asInstantResults(landingDashboard.data?.requests_served.data.result)),
    },
    {
      label: "Circuit Events",
      value: instantValue(asInstantResults(landingDashboard.data?.circuit_events.data.result)),
    },
    {
      label: "Rate Limited",
      value: instantValue(asInstantResults(landingDashboard.data?.rate_limited.data.result)),
    },
  ];

  return (
    <section className="flex min-h-[calc(100vh-73px)] items-center px-4 py-12 lg:px-8">
      <div className="mx-auto flex w-full max-w-[1280px] flex-col items-center gap-10">
        <div className="max-w-3xl text-center">
          <div className="text-xs uppercase tracking-[0.36em] text-text-muted">Production-grade gateway demo</div>
          <h1 className="mt-4 text-4xl font-semibold leading-tight text-text-primary md:text-5xl">
            Chaos injected. Telemetry live. Gateway behavior obvious in under 90 seconds.
          </h1>
          <p className="mt-4 text-lg leading-8 text-text-secondary">
            IronGate’s frontend is not a marketing layer. It is the mission-control surface for routing, retries, breakers,
            rate limiting, and traces under failure.
          </p>
        </div>

        <div className="w-full">
          <PipelineDiagram activeFlow={inFlight > 0} trafficIntensity={totalRps} errorRatio={errorRatio} />
        </div>

        <div className="grid w-full gap-4 md:grid-cols-3">
          {stats.map((stat) => (
            <div key={stat.label} className="panel-frame rounded-lg px-6 py-5 text-left">
              <div className="text-[11px] uppercase tracking-[0.26em] text-text-muted">{stat.label}</div>
              <div className="mt-3 font-mono text-3xl font-semibold text-text-primary font-tabular">{formatNumber(stat.value)}</div>
            </div>
          ))}
        </div>

        <Button asChild size="lg">
          <Link to="/chaos">Launch Observatory →</Link>
        </Button>
      </div>
    </section>
  );
}
