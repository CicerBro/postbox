import { useId, useState, type ReactNode } from "react";
import { CircleAlert, Copy, KeyRound } from "lucide-react";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import { Input } from "../ui/input";
import { Alert, AlertDescription } from "../ui/alert";
import { StatusMessage } from "../StatusMessage";
export function Section({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children: ReactNode;
}) {
  return (
    <section className="space-y-4">
      <div className="space-y-1 border-b pb-4">
        <h3 className="text-lg font-medium">{title}</h3>
        {description && (
          <p className="text-sm text-muted-foreground">{description}</p>
        )}
      </div>
      <div className="space-y-4">{children}</div>
    </section>
  );
}
export function Field({
  label,
  ...props
}: React.ComponentProps<typeof Input> & { label: string }) {
  return (
    <label className="block space-y-2 text-sm">
      <span className="font-medium">{label}</span>
      <Input {...props} />
    </label>
  );
}
export function Message({
  error,
  message,
}: {
  error?: unknown;
  message?: string;
}) {
  return error ? (
    <Alert
      className="border-red-200 bg-red-50 text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300"
    >
      <CircleAlert size={16} className="mt-1 shrink-0" aria-hidden="true" />
      <AlertDescription>{error instanceof Error ? error.message : String(error)}</AlertDescription>
    </Alert>
  ) : message ? (
    <StatusMessage>{message}</StatusMessage>
  ) : null;
}
export function useAction() {
  const [error, setError] = useState<unknown>();
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  async function run(fn: () => Promise<void>, success = "Saved.") {
    setBusy(true);
    setError(undefined);
    setMessage("");
    try {
      await fn();
      setMessage(success);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }
  return { error, message, busy, run };
}
export function Secret({
  value,
  onDismiss,
}: {
  value: string;
  onDismiss: () => void;
}) {
  const a = useAction();
  return (
    <div className="rounded-xl border border-amber-200 bg-amber-50/70 p-4 space-y-3 dark:border-amber-900 dark:bg-amber-950/20">
      <p className="text-sm font-medium">
        <KeyRound size={16} className="mr-2 inline" aria-hidden="true" />
        Copy this secret now. It will not be shown again.
      </p>
      <code className="block rounded-md border bg-background p-3 break-all text-sm">
        {value}
      </code>
      <div className="flex gap-2">
        <Button
          variant="outline"
          onClick={() =>
            a.run(() => navigator.clipboard.writeText(value), "Copied.")
          }
        >
          <Copy size={14} aria-hidden="true" />
          Copy
        </Button>
        <Button variant="ghost" onClick={onDismiss}>
          Dismiss
        </Button>
      </div>
      <Message {...a} />
    </div>
  );
}
export function date(value: number | null | undefined) {
  return value ? new Date(value).toLocaleString() : "Never";
}
export function strings(value: string | string[]) {
  if (Array.isArray(value)) return value;
  try {
    return JSON.parse(value) as string[];
  } catch {
    return [];
  }
}
export function Checks({
  values,
  selected,
  onChange,
}: {
  values: string[];
  selected: string[];
  onChange: (values: string[]) => void;
}) {
  const id = useId();
  return (
    <div className="flex flex-wrap gap-3">
      {values.map((v) => (
        <label
          key={v}
          htmlFor={`${id}-${v}`}
          className="flex items-center gap-3 rounded-lg border bg-background px-3 py-2 text-sm"
        >
          <Checkbox
            id={`${id}-${v}`}
            checked={selected.includes(v)}
            onCheckedChange={(checked) =>
              onChange(
                checked === true
                  ? [...selected, v]
                  : selected.filter((x) => x !== v),
              )
            }
          />
          {v.charAt(0).toUpperCase() + v.slice(1).replaceAll("_", " ")}
        </label>
      ))}
    </div>
  );
}
