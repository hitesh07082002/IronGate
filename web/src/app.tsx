import { Suspense, lazy } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter, Route, Routes } from "react-router-dom";

import { AppShell } from "@/components/layout/app-shell";

const queryClient = new QueryClient();

const LandingPage = lazy(async () => {
  const module = await import("@/pages/landing-page");
  return { default: module.LandingPage };
});
const AboutPage = lazy(async () => {
  const module = await import("@/pages/about-page");
  return { default: module.AboutPage };
});
const ChaosPage = lazy(async () => {
  const module = await import("@/pages/chaos-page");
  return { default: module.ChaosPage };
});
const ObservabilityPage = lazy(async () => {
  const module = await import("@/pages/observability-page");
  return { default: module.ObservabilityPage };
});

function RouteFallback() {
  return (
    <div className="mx-auto flex min-h-[60vh] max-w-[1280px] items-center justify-center px-4">
      <div className="panel-frame rounded-lg px-6 py-5 text-sm text-text-secondary">Loading observatory surface...</div>
    </div>
  );
}

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Suspense fallback={<RouteFallback />}>
          <Routes>
            <Route element={<AppShell />}>
              <Route path="/" element={<LandingPage />} />
              <Route path="/about" element={<AboutPage />} />
              <Route path="/chaos" element={<ChaosPage />} />
              <Route path="/observability" element={<ObservabilityPage />} />
            </Route>
          </Routes>
        </Suspense>
      </BrowserRouter>
    </QueryClientProvider>
  );
}
