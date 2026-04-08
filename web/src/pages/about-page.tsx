import { useState } from "react";

import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { PipelineDiagram } from "@/components/pipeline/pipeline-diagram";
import { ABOUT_DECISIONS, PIPELINE_NODES } from "@/lib/constants";

export function AboutPage() {
  const [activeNodeId, setActiveNodeId] = useState<string | null>(null);
  const activeNode = PIPELINE_NODES.find((node) => node.id === activeNodeId);

  return (
    <section className="px-4 py-10 lg:px-8">
      <div className="mx-auto max-w-[1280px] space-y-16">
        <div className="flex flex-wrap items-center gap-4 text-sm text-text-secondary">
          <a href="#problem" className="rounded-sm border border-ig-border px-3 py-2 hover:text-text-primary">
            The Problem
          </a>
          <a href="#pipeline" className="rounded-sm border border-ig-border px-3 py-2 hover:text-text-primary">
            Interactive Pipeline
          </a>
          <a href="#decisions" className="rounded-sm border border-ig-border px-3 py-2 hover:text-text-primary">
            Decisions
          </a>
        </div>

        <section id="problem" className="grid gap-8 lg:grid-cols-[1.1fr_0.9fr]">
          <div className="panel-frame rounded-lg p-6">
            <div className="text-xs uppercase tracking-[0.26em] text-text-muted">The Problem</div>
            <h1 className="mt-3 text-3xl font-semibold text-text-primary">Without a gateway, failure handling leaks into every service and every client.</h1>
            <div className="mt-4 space-y-4 text-base leading-8 text-text-secondary">
              <p>
                Each service would need to reinvent auth, retry rules, rate limiting, observability, load balancing, and
                circuit breaking. That makes failures harder to reason about and demo.
              </p>
              <p>
                IronGate centralizes those operational behaviors, then exposes them live so an interviewer can see how the
                system behaves when traffic turns hostile or infrastructure gets weird.
              </p>
            </div>
          </div>

          <div className="panel-frame rounded-lg p-6">
            <div className="grid gap-4 md:grid-cols-2">
              <div className="rounded-md border border-ev-error/30 bg-ev-error/5 p-4">
                <div className="text-xs uppercase tracking-[0.22em] text-ev-error">Before</div>
                <div className="mt-3 space-y-3 text-sm text-text-secondary">
                  <div>Auth logic duplicated across services.</div>
                  <div>Retries and breakers inconsistent or missing.</div>
                  <div>Rate limiting tightly coupled to app code.</div>
                  <div>Tracing and request correlation fragmented.</div>
                </div>
              </div>
              <div className="rounded-md border border-ev-success/30 bg-ev-success/5 p-4">
                <div className="text-xs uppercase tracking-[0.22em] text-ev-success">After</div>
                <div className="mt-3 space-y-3 text-sm text-text-secondary">
                  <div>Gateway owns the shared edge concerns.</div>
                  <div>Services focus on business behavior.</div>
                  <div>Failures show up coherently in metrics, traces, and logs.</div>
                  <div>Chaos scenarios become explicit, reproducible demos.</div>
                </div>
              </div>
            </div>
          </div>
        </section>

        <section id="pipeline" className="space-y-6">
          <div>
            <div className="text-xs uppercase tracking-[0.26em] text-text-muted">Interactive Pipeline</div>
            <h2 className="mt-3 text-3xl font-semibold text-text-primary">Eight decisions you can click through.</h2>
          </div>
          <PipelineDiagram interactive onNodeSelect={setActiveNodeId} />
        </section>

        <section id="decisions" className="space-y-6">
          <div>
            <div className="text-xs uppercase tracking-[0.26em] text-text-muted">Decision Cards</div>
            <h2 className="mt-3 text-3xl font-semibold text-text-primary">No icon grid, just the important tradeoffs.</h2>
          </div>
          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
            {ABOUT_DECISIONS.map((decision) => (
              <article key={decision.title} className="panel-frame flex h-full flex-col rounded-lg p-5">
                <div className="text-lg font-semibold text-text-primary">{decision.title}</div>
                <div className="mt-3 text-sm leading-6 text-text-secondary">{decision.verdict}</div>
                <div className="mt-4 border-t border-ig-border pt-4 text-sm leading-6 text-text-muted">{decision.tradeoff}</div>
                <a href={decision.href} target="_blank" rel="noreferrer" className="mt-5 text-sm font-medium text-ig-gateway">
                  Read ADR →
                </a>
              </article>
            ))}
          </div>
        </section>
      </div>

      <Sheet open={Boolean(activeNode)} onOpenChange={(open) => !open && setActiveNodeId(null)}>
        <SheetContent side="right">
          <SheetHeader>
            <SheetTitle>{activeNode?.label}</SheetTitle>
            <SheetDescription>{activeNode?.summary}</SheetDescription>
          </SheetHeader>
          {activeNode && (
            <div className="mt-6 space-y-6 text-sm leading-7 text-text-secondary">
              <div>
                <div className="text-xs uppercase tracking-[0.22em] text-text-muted">Why it exists</div>
                <div className="mt-2">{activeNode.why}</div>
              </div>
              <div>
                <div className="text-xs uppercase tracking-[0.22em] text-text-muted">Failure mode</div>
                <div className="mt-2">{activeNode.failure}</div>
              </div>
              <div>
                <div className="text-xs uppercase tracking-[0.22em] text-text-muted">ADR</div>
                <a href={activeNode.adr.href} target="_blank" rel="noreferrer" className="mt-2 inline-block text-ig-gateway">
                  {activeNode.adr.title} →
                </a>
              </div>
            </div>
          )}
        </SheetContent>
      </Sheet>
    </section>
  );
}
