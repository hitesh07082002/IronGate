import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";

import { cn } from "@/lib/utils";

const buttonVariants = cva(
  "inline-flex h-11 items-center justify-center whitespace-nowrap rounded-md border border-ig-border px-4 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ig-gateway focus-visible:ring-offset-2 focus-visible:ring-offset-ig-bg disabled:pointer-events-none disabled:opacity-45",
  {
    variants: {
      variant: {
        default: "bg-ig-gateway text-text-primary hover:bg-blue-400",
        secondary: "bg-ig-surface-elevated text-text-primary hover:bg-zinc-800",
        ghost: "bg-transparent text-text-secondary hover:border-zinc-700 hover:text-text-primary",
        danger: "bg-ev-error text-text-primary hover:bg-red-500",
      },
      size: {
        default: "px-4",
        sm: "h-9 rounded-sm px-3 text-xs",
        lg: "h-12 rounded-md px-5 text-base",
        icon: "h-11 w-11 px-0",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  },
);

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean;
}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : "button";
    return <Comp className={cn(buttonVariants({ variant, size, className }))} ref={ref} {...props} />;
  },
);

Button.displayName = "Button";

export { Button, buttonVariants };
