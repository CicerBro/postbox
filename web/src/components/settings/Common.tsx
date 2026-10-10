import { useState, type ReactNode } from "react";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
export function Section({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">{children}</CardContent>
    </Card>
  );
}
export function Field({
  label,
  ...props
}: React.ComponentProps<typeof Input> & { label: string }) {
  return (
    <label className="block space-y-2 text-sm">
      <span>{label}</span>
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
    <p role="alert" className="text-sm text-destructive">
      {error instanceof Error ? error.message : String(error)}
    </p>
  ) : message ? (
    <p role="status" className="text-sm">
      {message}
    </p>
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
    <div className="rounded-lg border p-4 space-y-3">
      <p className="text-sm font-medium">
        Copy this secret now. It will not be shown again.
      </p>
      <code className="block break-all text-sm">{value}</code>
      <div className="flex gap-2">
        <Button
          variant="outline"
          onClick={() =>
            a.run(() => navigator.clipboard.writeText(value), "Copied.")
          }
        >
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
  return (
    <div className="flex flex-wrap gap-4">
      {values.map((v) => (
        <label key={v} className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={selected.includes(v)}
            onChange={(e) =>
              onChange(
                e.target.checked
                  ? [...selected, v]
                  : selected.filter((x) => x !== v),
              )
            }
          />
          {v.replaceAll("_", " ")}
        </label>
      ))}
    </div>
  );
}
