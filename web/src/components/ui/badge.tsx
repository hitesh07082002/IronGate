import { cva, type VariantProps } from "class-variance-authority";
import type { HTMLAttributes } from "react";

import { cn } from "@/lib/utils";

const badgeVariants = cva(
  "inline-flex items-center rounded-sm border px-2.5 py-1 text-[11px] font-medium uppercase tracking-[0.18em]",
  {
    variants: {
      variant: {
        default: "border-ig-border bg-ig-surface-elevated text-text-secondary",
        success: "border-ev-success/40 bg-ev-success/10 text-ev-success",
        warning: "border-ev-warning/40 bg-ev-warning/10 text-ev-warning",
        error: "border-ev-error/40 bg-ev-error/10 text-ev-error",
        system: "border-ig-gateway/40 bg-ig-gateway/10 text-ig-gateway",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  },
);

export interface BadgeProps extends HTMLAttributes<HTMLDivElement>, VariantProps<typeof badgeVariants> {}

export function Badge({ className, variant, ...props }: BadgeProps) {
  return <div className={cn(badgeVariants({ variant }), className)} {...props} />;
}
