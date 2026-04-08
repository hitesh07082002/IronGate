import { useQuery } from "@tanstack/react-query";

import { fetchPrometheus } from "@/lib/api";
import type { PrometheusMode } from "@/types/observatory";

interface PrometheusQueryOptions {
  enabled?: boolean;
  mode?: PrometheusMode;
  pollMs?: number;
  rangeSeconds?: number;
}

export function usePrometheusQuery(query: string, stepSeconds: number, options?: PrometheusQueryOptions) {
  const mode = options?.mode ?? "instant";
  const rangeSeconds = options?.rangeSeconds ?? 120;

  return useQuery({
    queryKey: ["prometheus", mode, query, stepSeconds, rangeSeconds],
    queryFn: () => fetchPrometheus(mode, query, stepSeconds, rangeSeconds),
    enabled: (options?.enabled ?? true) && query.trim() !== "",
    refetchInterval: options?.pollMs ?? 2000,
    staleTime: 1000,
  });
}
