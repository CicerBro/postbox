import type { ReactNode } from "react";
import { CheckCircle2, Info } from "lucide-react";
import { Alert, AlertDescription } from "./ui/alert";

export function StatusMessage({
  children,
  variant = "success",
}: {
  children: ReactNode;
  variant?: "success" | "info";
}) {
  if (!children) return null;
  const Icon = variant === "success" ? CheckCircle2 : Info;
  return (
    <Alert
      role="status"
      className={variant === "success"
        ? "border-green-200 bg-green-50 text-green-800 dark:border-green-800 dark:bg-green-950/40 dark:text-green-200"
        : "border-blue-200 bg-blue-50 text-blue-800 dark:border-blue-800 dark:bg-blue-950/40 dark:text-blue-200"}
    >
      <Icon size={16} className="mt-1 shrink-0" aria-hidden="true" />
      <AlertDescription>{children}</AlertDescription>
    </Alert>
  );
}
