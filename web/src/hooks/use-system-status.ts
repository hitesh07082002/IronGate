import { useQuery } from "@tanstack/react-query";

import { fetchHealth } from "@/lib/api";

export function useSystemStatus() {
  return useQuery({
    queryKey: ["system-health"],
    queryFn: fetchHealth,
    refetchInterval: 3000,
    staleTime: 1000,
  });
}
