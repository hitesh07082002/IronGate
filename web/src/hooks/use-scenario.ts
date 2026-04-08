import { startTransition, useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { fetchScenario, fetchScenarios, fetchScenarioStatuses, resetSystem, runScenario, stopScenario } from "@/lib/api";
import { INTENSITY_ORDER } from "@/lib/constants";
import type { ScenarioStatus } from "@/types/observatory";

export function useScenario() {
  const queryClient = useQueryClient();
  const [selectedName, setSelectedName] = useState<string | null>(null);
  const [selectedIntensity, setSelectedIntensity] = useState<string>("moderate");
  const [selectedDuration, setSelectedDuration] = useState<number>(60);
  const [lastResetResult, setLastResetResult] = useState<string | null>(null);

  const scenariosQuery = useQuery({
    queryKey: ["scenarios"],
    queryFn: fetchScenarios,
    staleTime: 10_000,
    refetchInterval: 10_000,
  });

  useEffect(() => {
    const names = scenariosQuery.data?.map((scenario) => scenario.name) ?? [];
    if (names.length === 0) {
      if (selectedName !== null) {
        startTransition(() => {
          setSelectedName(null);
        });
      }
      return;
    }

    if (!selectedName || !names.includes(selectedName)) {
      startTransition(() => {
        setSelectedName(names[0]);
      });
    }
  }, [selectedName, scenariosQuery.data]);

  const scenarioQuery = useQuery({
    queryKey: ["scenario", selectedName],
    queryFn: () => fetchScenario(selectedName ?? ""),
    enabled: Boolean(selectedName),
    staleTime: 10_000,
  });

  useEffect(() => {
    const scenario = scenarioQuery.data;
    if (!scenario) {
      return;
    }

    if (!scenario.intensity_options[selectedIntensity]) {
      const fallback =
        INTENSITY_ORDER.find((value) => scenario.intensity_options[value]) ??
        Object.keys(scenario.intensity_options)[0] ??
        "moderate";
      setSelectedIntensity(fallback);
    }

    if (!scenario.duration_options.includes(selectedDuration)) {
      setSelectedDuration(scenario.duration_options[0] ?? 60);
    }
  }, [scenarioQuery.data, selectedDuration, selectedIntensity]);

  const statusesQuery = useQuery({
    queryKey: ["scenario-statuses", scenariosQuery.data?.map((scenario) => scenario.name).join(",") ?? ""],
    queryFn: () => fetchScenarioStatuses((scenariosQuery.data ?? []).map((scenario) => scenario.name)),
    enabled: Boolean(scenariosQuery.data?.length),
    refetchInterval: 2000,
    staleTime: 1000,
  });

  const runningEntry = Object.entries(statusesQuery.data ?? {}).find(([, status]) =>
    status === "running" || status === "stopping",
  );
  const runningScenarioName = runningEntry?.[0] ?? null;
  const runningScenarioStatus = runningEntry?.[1] as ScenarioStatus | undefined;

  useEffect(() => {
    if (!runningScenarioName || runningScenarioName === selectedName) {
      return;
    }

    startTransition(() => {
      setSelectedName(runningScenarioName);
    });
  }, [runningScenarioName, selectedName]);

  const runMutation = useMutation({
    mutationFn: (input: { name: string; intensity: string; duration: number }) =>
      runScenario(input.name, input.intensity, input.duration),
    onSuccess: async () => {
      setLastResetResult(null);
      await queryClient.invalidateQueries({ queryKey: ["scenario-statuses"] });
    },
  });

  const stopMutation = useMutation({
    mutationFn: (name: string) => stopScenario(name),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["scenario-statuses"] });
    },
  });

  const resetMutation = useMutation({
    mutationFn: resetSystem,
    onSuccess: async (payload) => {
      setLastResetResult(payload.status);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["scenario-statuses"] }),
        queryClient.invalidateQueries({ queryKey: ["system-health"] }),
      ]);
    },
  });

  return {
    selectedName,
    setSelectedName: (name: string) => {
      startTransition(() => setSelectedName(name));
    },
    selectedIntensity,
    setSelectedIntensity,
    selectedDuration,
    setSelectedDuration,
    lastResetResult,
    scenario: scenarioQuery.data,
    scenarioError: scenarioQuery.error,
    scenarioLoading: scenarioQuery.isLoading,
    scenarios: scenariosQuery.data ?? [],
    scenariosError: scenariosQuery.error,
    scenariosLoading: scenariosQuery.isLoading,
    scenarioStatuses: statusesQuery.data ?? {},
    statusesLoading: statusesQuery.isLoading,
    runningScenarioName,
    runningScenarioStatus,
    runScenario: runMutation.mutateAsync,
    running: runMutation.isPending,
    stopScenario: stopMutation.mutateAsync,
    stopping: stopMutation.isPending,
    resetSystem: resetMutation.mutateAsync,
    resetting: resetMutation.isPending,
  };
}
