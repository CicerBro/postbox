import { Badge } from "../components/ui/badge";
import { api } from "../lib/api";
export type Campaign = {
  id: number;
  subject: string;
  html: string;
  text: string | null;
  inline_css: number | boolean;
  status: string;
  created_at: number;
  scheduled_at: number | null;
  created_by: number | null;
};
export type Template = {
  id: number;
  name: string;
  html: string;
  updated_at: number;
};
export type Named = { id: number; name: string };
export type Target = {
  target_type: "list" | "segment";
  target_id: number;
  mode: "include" | "exclude";
};
export const initialHTML =
  '<!DOCTYPE html>\n<html>\n<head>\n<meta charset="utf-8">\n<meta name="viewport" content="width=device-width, initial-scale=1">\n<style>body { font-family: Arial, sans-serif; }</style>\n</head>\n<body>\n<p>Hello [Name,fallback=there],</p>\n<p>Your newsletter goes here.</p>\n<p><a href="[unsubscribe]">Unsubscribe</a> · <a href="[webversion]">View online</a></p>\n</body>\n</html>';
export const tableClass =
  "w-full text-sm [&_th]:text-left [&_th]:font-medium [&_th]:p-3 [&_td]:p-3 [&_tr]:border-b";
export const selectClass =
  "rounded-md border bg-background px-3 py-2 text-sm w-full";
export { errorText, ErrorMessage } from "../components/RequestError";
export { allPages as allRows } from "../lib/pagination";
export const number = (n: number) => new Intl.NumberFormat().format(n);
export const bytes = (n: number) =>
  n >= 1048576
    ? `${(n / 1048576).toFixed(1)} MB`
    : `${(n / 1024).toFixed(1)} KB`;

export function StatusBadge({ status }: { status: string }) {
  const tone = ["sent", "delivered", "completed", "finished"].includes(status)
    ? "success"
    : ["failed", "cancelled", "canceled"].includes(status)
      ? "danger"
      : ["paused", "unknown", "scheduled"].includes(status)
        ? "warning"
        : ["sending", "preparing", "claimed"].includes(status)
          ? "info"
          : "neutral";
  return (
    <Badge variant="outline" className={`status-badge status-${tone}`}>
      <span className="status-dot" aria-hidden="true" />
      {status}
    </Badge>
  );
}
export function contentExcerpt(html: string) {
  return (html ?? "")
    .replace(/<head[\s\S]*?<\/head>/gi, " ")
    .replace(/<style[\s\S]*?<\/style>/gi, " ")
    .replace(/<script[\s\S]*?<\/script>/gi, " ")
    .replace(/<[^>]*>/g, " ")
    .replace(/\s+/g, " ")
    .trim()
    .slice(0, 110);
}
