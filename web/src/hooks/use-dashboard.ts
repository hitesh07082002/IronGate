import { useQuery } from "@tanstack/react-query";

import { fetchChaosDashboard, fetchLandingDashboard } from "@/lib/api";

export function useLandingDashboard() {
  return useQuery({
    queryKey: ["dashboard", "landing"],
    queryFn: fetchLandingDashboard,
    refetchInterval: 2000,
    staleTime: 1000,
  });
}

export function useChaosDashboard() {
  return useQuery({
    queryKey: ["dashboard", "chaos"],
    queryFn: fetchChaosDashboard,
    refetchInterval: 2000,
    staleTime: 1000,
  });
}
