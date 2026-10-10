import type { HTMLAttributes } from "react";
import { cn } from "../../lib/utils";
export function Badge({
  className,
  variant,
  ...props
}: HTMLAttributes<HTMLSpanElement> & { variant?: string }) {
  return (
    <span
      className={cn(
        "badge",
        variant === "destructive" && "text-red-600",
        className,
      )}
      {...props}
    />
  );
}
