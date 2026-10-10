import type { HTMLAttributes } from "react";
import { cn } from "../../lib/utils";
export function Badge({
  className,
  variant,
  ...props
}: HTMLAttributes<HTMLSpanElement> & { variant?: string }) {
  const tones: Record<string, string> = {
    success: "status-success",
    warning: "status-warning",
    destructive: "status-danger",
    secondary: "status-neutral",
    info: "status-info",
  };
  return (
    <span
      className={cn("badge", variant && tones[variant], className)}
      {...props}
    />
  );
}
