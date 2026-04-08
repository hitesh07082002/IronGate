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

  return (
    <div className="panel-frame relative h-[22rem] overflow-hidden rounded-lg">
      <iframe
        title={title}
        src={src}
        className="h-full w-full border-0 bg-ig-surface"
        style={{ visibility: loaded ? "visible" : "hidden" }}
        onLoad={() => setLoaded(true)}
      />
      {!loaded && (
        <div className="absolute inset-0 flex items-center justify-center px-6 text-center">
          <div>
            <div className="text-lg font-medium text-text-primary">
              {timedOut ? `${title} is blocked in an iframe` : `Loading ${title}`}
            </div>
            <div className="mt-2 text-sm text-text-secondary">
              {timedOut
                ? "Grafana embedding may be unavailable in this environment. The direct link still works."
                : "Connecting to Grafana and waiting for the panel to render."}
            </div>
            <Button className="mt-4" asChild variant={timedOut ? "default" : "secondary"}>
              <a href={fallbackHref} target="_blank" rel="noreferrer">
                Open in Grafana →
              </a>
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
