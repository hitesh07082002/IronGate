import { useEffect, useState } from "react";

import { cn } from "@/lib/utils";

interface PipelineDiagramProps {
  activeFlow?: boolean;
  errorRatio?: number;
  interactive?: boolean;
  onNodeSelect?: (nodeId: string) => void;
  trafficIntensity?: number;
}

interface PipelineNode {
  id: string;
  label: string;
  x: number;
  y: number;
  width: number;
  height: number;
  stroke: string;
  fill: string;
}

const heroNodes: PipelineNode[] = [
  { id: "gateway", label: "Gateway", x: 32, y: 74, width: 110, height: 44, stroke: "#3B82F6", fill: "#0f172a" },
  { id: "router", label: "Router", x: 180, y: 74, width: 110, height: 44, stroke: "#3B82F6", fill: "#101828" },
  { id: "auth", label: "Auth", x: 328, y: 74, width: 110, height: 44, stroke: "#3B82F6", fill: "#101828" },
  { id: "rate-limit", label: "Rate Limit", x: 476, y: 74, width: 126, height: 44, stroke: "#F97316", fill: "#23160d" },
  { id: "proxy", label: "Proxy", x: 640, y: 74, width: 110, height: 44, stroke: "#3B82F6", fill: "#101828" },
  { id: "services", label: "Services", x: 788, y: 74, width: 118, height: 44, stroke: "#10B981", fill: "#0d1d18" },
  { id: "prometheus", label: "Prometheus", x: 476, y: 182, width: 126, height: 40, stroke: "#8B5CF6", fill: "#1d1430" },
  { id: "grafana", label: "Grafana", x: 640, y: 182, width: 110, height: 40, stroke: "#8B5CF6", fill: "#1d1430" },
  { id: "tempo", label: "Tempo", x: 788, y: 182, width: 118, height: 40, stroke: "#8B5CF6", fill: "#1d1430" },
];

const interactiveNodes: PipelineNode[] = [
  { id: "config", label: "Config", x: 28, y: 28, width: 116, height: 44, stroke: "#71717A", fill: "#18181B" },
  { id: "router", label: "Router", x: 188, y: 28, width: 116, height: 44, stroke: "#3B82F6", fill: "#0f172a" },
  { id: "auth", label: "Auth", x: 348, y: 28, width: 116, height: 44, stroke: "#3B82F6", fill: "#0f172a" },
  { id: "rate-limit", label: "Rate Limit", x: 508, y: 28, width: 126, height: 44, stroke: "#F97316", fill: "#23160d" },
  { id: "retry", label: "Retry", x: 188, y: 154, width: 116, height: 44, stroke: "#3B82F6", fill: "#0f172a" },
  { id: "load-balancer", label: "Least Conn", x: 348, y: 154, width: 132, height: 44, stroke: "#10B981", fill: "#0d1d18" },
  { id: "circuit-breaker", label: "Circuit", x: 524, y: 154, width: 118, height: 44, stroke: "#EF4444", fill: "#2a1313" },
  { id: "observability", label: "Observe", x: 700, y: 92, width: 118, height: 44, stroke: "#8B5CF6", fill: "#1d1430" },
];

function nodeTextColor(stroke: string) {
  return stroke === "#71717A" ? "#A1A1AA" : "#FAFAFA";
}

export function PipelineDiagram({
  activeFlow = false,
  errorRatio = 0,
  interactive = false,
  onNodeSelect,
  trafficIntensity = 0,
}: PipelineDiagramProps) {
  const [tick, setTick] = useState<number>(Date.now());
  const nodes = interactive ? interactiveNodes : heroNodes;

  useEffect(() => {
    if (!activeFlow) {
      setTick(Date.now());
      return;
    }

    let frame = 0;
    const animate = () => {
      setTick(Date.now());
      frame = window.requestAnimationFrame(animate);
    };

    frame = window.requestAnimationFrame(animate);
    return () => window.cancelAnimationFrame(frame);
  }, [activeFlow]);

  const mainLineLength = interactive ? 498 : 770;
  const startX = interactive ? 144 : 142;
  const dotY = interactive ? 50 : 96;
  const speedMultiplier = Math.max(1, trafficIntensity / 24);
  const phase = ((tick / 1000) * 110 * speedMultiplier) % mainLineLength;
  const dotColor = errorRatio > 0.12 ? "#EF4444" : "#3B82F6";

  return (
    <div className="panel-frame grid-scan overflow-hidden rounded-lg p-4">
      <svg
        viewBox={interactive ? "0 0 860 232" : "0 0 940 260"}
        role="img"
        aria-label={interactive ? "Interactive IronGate pipeline" : "Animated IronGate pipeline"}
        className="w-full"
      >
        {!interactive && (
          <>
            <path d="M142 96H788" stroke="#1F1F23" strokeWidth="8" strokeLinecap="round" />
            <path d="M540 118V182H902" stroke="#1F1F23" strokeWidth="8" strokeLinecap="round" />
          </>
        )}
        {interactive && (
          <>
            <path d="M144 50H508" stroke="#1F1F23" strokeWidth="8" strokeLinecap="round" />
            <path d="M566 72V154" stroke="#1F1F23" strokeWidth="8" strokeLinecap="round" />
            <path d="M246 154H524" stroke="#1F1F23" strokeWidth="8" strokeLinecap="round" />
            <path d="M642 176H700V114" stroke="#1F1F23" strokeWidth="8" strokeLinecap="round" />
            <path d="M408 72V154" stroke="#1F1F23" strokeWidth="8" strokeLinecap="round" />
          </>
        )}

        {nodes.map((node) => (
          <g
            key={node.id}
            className={cn(onNodeSelect && "cursor-pointer")}
            onClick={() => onNodeSelect?.(node.id)}
            onKeyDown={(event) => {
              if (event.key === "Enter" || event.key === " ") {
                event.preventDefault();
                onNodeSelect?.(node.id);
              }
            }}
            role={onNodeSelect ? "button" : undefined}
            tabIndex={onNodeSelect ? 0 : undefined}
          >
            <rect
              x={node.x}
              y={node.y}
              width={node.width}
              height={node.height}
              rx="6"
              fill={node.fill}
              stroke={node.stroke}
              strokeWidth="1.5"
              className={cn(!activeFlow && "animate-pulse-glow")}
            />
            <text
              x={node.x + node.width / 2}
              y={node.y + node.height / 2 + 5}
              textAnchor="middle"
              fill={nodeTextColor(node.stroke)}
              fontSize="14"
              fontWeight="600"
              fontFamily="Geist, system-ui, sans-serif"
            >
              {node.label}
            </text>
          </g>
        ))}

        {[0, 1, 2].map((index) => {
          const x = startX + ((phase + index * (mainLineLength / 3)) % mainLineLength);
          return (
            <circle
              key={`main-${index}`}
              cx={x}
              cy={dotY}
              r={activeFlow ? 5 : 4}
              fill={dotColor}
              opacity={activeFlow ? 0.95 : 0.3}
            />
          );
        })}

        {!interactive &&
          [0, 1].map((index) => {
            const x = 540 + (((phase * 0.58) + index * 164) % 360);
            return <circle key={`observe-${index}`} cx={x} cy={204} r="4.5" fill="#8B5CF6" opacity={activeFlow ? 0.9 : 0.32} />;
          })}
      </svg>
    </div>
  );
}
