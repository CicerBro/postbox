import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { useBrandPermission } from "../../lib/use-brand-permission";
import type { Subscriber as ContractSubscriber } from "../../lib/generated-contracts";

export type SubscriberList = {
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
export type Subscriber = Omit<
  Pick<
    ContractSubscriber,
    | "id"
    | "email"
    | "name"
    | "status"
    | "fields"
    | "gdpr_consent_at"
    | "marketing_unsubscribed_at"
    | "gdpr_consent_ip"
    | "created_at"
    | "updated_at"
    | "last_activity_at"
    | "last_activity_kind"
  >,
  "fields"
> & { fields: ContractSubscriber["fields"] | string };
export const tableClass =
  "w-full text-sm [&_th]:text-left [&_th]:font-medium [&_th]:text-muted-foreground [&_td]:py-2 [&_th]:h-10 [&_th]:py-2 [&_tr]:border-b [&_tbody_tr:last-child]:border-0 [&_td]:px-3 [&_th]:px-3";
export function subscriberInitials(name: string, email: string) {
  const words = (name.trim() || email).split(/\s+/);
  return (
    words.length > 1
      ? words[0][0] + words[words.length - 1][0]
      : words[0].slice(0, 2)
  ).toUpperCase();
}
export { ErrorMessage } from "../RequestError";
export function useSubscribersQuery<T>(
  brandId: number,
  resource: string,
  path = `/${resource}`,
  enabled = true,
) {
  return useQuery({
    queryKey: ["subscribers", brandId, resource, path],
    queryFn: ({ signal }) => api<T>(path, { brandId, signal }),
    enabled: brandId > 0 && enabled,
  });
}
export function useCanWriteSubscribers(brandId: number) {
  return useBrandPermission(brandId, "write");
}
export function useSubscriberAction<T = unknown>(brandId: number) {
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
  value: Subscriber["fields"] | string,
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
export { allPages } from "../../lib/pagination";
