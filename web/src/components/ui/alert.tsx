import type { HTMLAttributes } from "react";
import { cn } from "../../lib/utils";

export function Alert({
  className,
  role = "alert",
  ...props
}: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      role={role}
      data-slot="alert"
      className={cn("flex w-full items-start gap-3 rounded-lg border p-4 text-sm", className)}
      {...props}
    />
  );
}

export function AlertDescription({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      data-slot="alert-description"
      className={cn("min-w-0 flex-1 break-words leading-relaxed", className)}
      {...props}
    />
  );
}
