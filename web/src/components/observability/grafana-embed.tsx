import { useEffect, useState } from "react";

import { Button } from "@/components/ui/button";

interface GrafanaEmbedProps {
  fallbackHref: string;
  src: string;
  title: string;
}

export function GrafanaEmbed({ fallbackHref, src, title }: GrafanaEmbedProps) {
  const [loaded, setLoaded] = useState(false);
  const [timedOut, setTimedOut] = useState(false);

  useEffect(() => {
    setLoaded(false);
    setTimedOut(false);
    const timer = window.setTimeout(() => setTimedOut(true), 25000);
    return () => window.clearTimeout(timer);
  }, [src]);

  if (!loaded && !timedOut) {
    return (
      <div className="panel-frame flex h-[22rem] items-center justify-center rounded-lg px-6 text-center">
        <div>
          <div className="text-lg font-medium text-text-primary">Loading {title}</div>
          <div className="mt-2 text-sm text-text-secondary">Connecting to Grafana and waiting for the panel to render.</div>
          <Button className="mt-4" asChild variant="secondary">
            <a href={fallbackHref} target="_blank" rel="noreferrer">
              Open in Grafana →
            </a>
          </Button>
        </div>
      </div>
    );
  }

  if (timedOut && !loaded) {
    return (
      <div className="panel-frame flex h-[22rem] items-center justify-center rounded-lg px-6 text-center">
        <div>
          <div className="text-lg font-medium text-text-primary">{title} is blocked in an iframe</div>
          <div className="mt-2 text-sm text-text-secondary">
            Grafana embedding may be unavailable in this environment. The direct link still works.
          </div>
          <Button className="mt-4" asChild>
            <a href={fallbackHref} target="_blank" rel="noreferrer">
              Open in Grafana →
            </a>
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className="panel-frame overflow-hidden rounded-lg">
      <iframe
        title={title}
        src={src}
        className="h-[22rem] w-full border-0 bg-ig-surface"
        onLoad={() => setLoaded(true)}
      />
    </div>
  );
}
