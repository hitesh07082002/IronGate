import { useState } from "react";
import { Link, NavLink } from "react-router-dom";

import { Button } from "@/components/ui/button";
import { Sheet, SheetClose, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { NAV_ITEMS, REPO_URL } from "@/lib/constants";
import { cn } from "@/lib/utils";

function DesktopNav() {
  return (
    <div className="hidden items-center gap-8 lg:flex">
      {NAV_ITEMS.map((item) => (
        <NavLink
          key={item.to}
          to={item.to}
          className={({ isActive }) =>
            cn(
              "border-b-2 border-transparent pb-1 text-sm font-medium text-text-secondary transition hover:text-text-primary",
              isActive && "border-ig-gateway text-text-primary",
            )
          }
        >
          {item.label}
        </NavLink>
      ))}
      <a
        href={REPO_URL}
        target="_blank"
        rel="noreferrer"
        className="border-b-2 border-transparent pb-1 text-sm font-medium text-text-secondary transition hover:text-text-primary"
      >
        GitHub ↗
      </a>
    </div>
  );
}

export function SiteNav() {
  const [open, setOpen] = useState(false);

  return (
    <header className="sticky top-0 z-40 border-b border-ig-border/80 bg-ig-bg/95 backdrop-blur">
      <div className="mx-auto flex max-w-[1280px] items-center justify-between px-4 py-4 lg:px-8">
        <Link to="/" className="flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-md border border-ig-border bg-ig-surface-elevated text-sm font-semibold text-ig-gateway">
            IG
          </div>
          <div>
            <div className="text-sm font-semibold uppercase tracking-[0.28em] text-text-secondary">IronGate</div>
            <div className="text-xs text-text-muted">Chaos Observatory</div>
          </div>
        </Link>

        <DesktopNav />

        <div className="lg:hidden">
          <Sheet open={open} onOpenChange={setOpen}>
            <SheetTrigger asChild>
              <Button variant="ghost" size="icon" aria-label="Open navigation">
                ☰
              </Button>
            </SheetTrigger>
            <SheetContent side="right" className="w-[min(88vw,18rem)]">
              <div className="flex flex-col gap-6">
                <SheetHeader>
                  <div className="text-sm font-semibold uppercase tracking-[0.28em] text-text-secondary">IronGate</div>
                  <SheetTitle className="mt-1">Mission Control</SheetTitle>
                  <SheetDescription>Navigate between the about, observatory, and observability views.</SheetDescription>
                </SheetHeader>
                <nav className="flex flex-col gap-2" aria-label="Mobile">
                  {NAV_ITEMS.map((item) => (
                    <SheetClose asChild key={item.to}>
                      <NavLink
                        to={item.to}
                        className={({ isActive }) =>
                          cn(
                            "rounded-md border border-ig-border px-4 py-3 text-sm text-text-secondary transition hover:text-text-primary",
                            isActive && "bg-ig-surface-elevated text-text-primary",
                          )
                        }
                      >
                        {item.label}
                      </NavLink>
                    </SheetClose>
                  ))}
                  <a
                    href={REPO_URL}
                    target="_blank"
                    rel="noreferrer"
                    className="rounded-md border border-ig-border px-4 py-3 text-sm text-text-secondary transition hover:text-text-primary"
                  >
                    GitHub ↗
                  </a>
                </nav>
              </div>
            </SheetContent>
          </Sheet>
        </div>
      </div>
    </header>
  );
}
