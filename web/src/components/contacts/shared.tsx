import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../lib/api";
import type { ReactNode } from "react";
import { Badge } from "../ui/badge";

export type ContactList = {
  id: number;
  name: string;
  uuid: string;
  opt_in: string;
  active?: number;
  subscribed?: number;
  unsubscribed?: number;
  bounced?: number;
  complained?: number;
};
export type CustomField = {
  id: number;
  key: string;
  label: string;
  type: string;
  is_filterable: boolean;
};
export type Subscriber = {
  id: number;
  email: string;
  name: string;
  status: string;
  fields: Record<string, unknown> | string;
  gdpr_consent_at?: number;
  gdpr_consent_ip?: string;
  created_at: number;
  updated_at: number;
};
export const selectClass =
  "h-9 rounded-md border border-input bg-background px-3 text-sm shadow-xs w-full";
export const tableClass =
  "w-full text-sm [&_th]:text-left [&_th]:font-medium [&_th]:text-muted-foreground [&_td]:py-3 [&_th]:h-10 [&_th]:py-2 [&_tr]:border-b [&_tbody_tr]:hover:bg-muted/50 [&_tbody_tr:last-child]:border-0 [&_td]:px-3 [&_th]:px-3";
export function ContactStatusBadge({ status }: { status: string }) {
  return (
    <Badge variant="outline" className={`status-badge status-${status}`}>
      <span className="status-dot" />
      {status}
    </Badge>
  );
}
export function contactInitials(name: string, email: string) {
  const words = (name.trim() || email).split(/\s+/);
  return (
    words.length > 1
      ? words[0][0] + words[words.length - 1][0]
      : words[0].slice(0, 2)
  ).toUpperCase();
}
export function ErrorMessage({ error }: { error: unknown }) {
  const status =
    error && typeof error === "object" && "status" in error
      ? Number(error.status)
      : undefined;
  return error ? (
    <p role="alert" className="text-sm text-destructive">
      {status === 403 ? "Access denied (403): " : ""}
      {error instanceof Error ? error.message : String(error)}
    </p>
  ) : null;
}
export function Field({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <label className="grid gap-2 text-sm font-medium">
      {label}
      {children}
    </label>
  );
}
export function useContactsQuery<T>(
  brandId: number,
  resource: string,
  path = `/${resource}`,
  enabled = true,
) {
  return useQuery({
    queryKey: ["contacts", brandId, resource, path],
    queryFn: ({ signal }) => api<T>(path, { brandId, signal }),
    enabled: brandId > 0 && enabled,
  });
}
export function useContactAction<T = unknown>(brandId: number) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (v: { path: string; method?: string; body?: unknown }) =>
      api<T>(v.path, { brandId, method: v.method ?? "POST", body: v.body }),
    onSuccess: async () => {
      await client.invalidateQueries({
        predicate: (q) => q.queryKey.includes(brandId),
      });
    },
  });
}
export function parseFields(
  value: Subscriber["fields"],
): Record<string, unknown> {
  const fields = typeof value === "string" ? JSON.parse(value) : value;
  return fields && typeof fields === "object" && !Array.isArray(fields)
    ? fields
    : {};
}
export function fieldValue(type: string, value: string): unknown {
  if (value === "") return null;
  if (type === "number") {
    const n = Number(value);
    if (!Number.isFinite(n)) throw new Error("Enter a finite number.");
    return n;
  }
  if (type === "boolean") return value === "true";
  return value;
}
// Handles quoted commas, CRLF, embedded newlines and escaped quotes without changing the uploaded file.
export function parseCSV(text: string, maxRows = Infinity): string[][] {
  const rows: string[][] = [];
  let row: string[] = [],
    cell = "",
    quoted = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (c === '"') {
      if (quoted && text[i + 1] === '"') {
        cell += '"';
        i++;
      } else quoted = !quoted;
    } else if (c === "," && !quoted) {
      row.push(cell);
      cell = "";
    } else if ((c === "\n" || c === "\r") && !quoted) {
      if (c === "\r" && text[i + 1] === "\n") i++;
      row.push(cell);
      rows.push(row);
      row = [];
      cell = "";
      if (rows.length >= maxRows) return rows;
    } else cell += c;
  }
  if (quoted) throw new Error("CSV contains an unclosed quoted value.");
  if (cell || row.length) {
    row.push(cell);
    rows.push(row);
  }
  return rows;
}
export async function allPages<T extends { id: number }>(
  path: string,
  brandId: number,
  signal?: AbortSignal,
): Promise<T[]> {
  const out: T[] = [];
  let after = 0;
  for (;;) {
    const page = await api<T[]>(
      `${path}${path.includes("?") ? "&" : "?"}limit=500&after=${after}`,
      { brandId, signal },
    );
    out.push(...page);
    if (page.length < 500) return out;
    after = page[page.length - 1].id;
  }
}
