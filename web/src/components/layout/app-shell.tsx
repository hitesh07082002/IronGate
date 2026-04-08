import { Outlet } from "react-router-dom";

import { SiteNav } from "@/components/layout/site-nav";

export function AppShell() {
  return (
    <div className="min-h-screen bg-ig-bg text-text-primary">
      <a href="#main-content" className="sr-only-focusable rounded-md bg-ig-gateway px-4 py-2 text-sm font-medium text-text-primary">
        Skip to content
      </a>
      <SiteNav />
      <main id="main-content">
        <Outlet />
      </main>
    </div>
  );
}
