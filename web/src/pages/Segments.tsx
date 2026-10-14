import { ConfirmDialog } from "../components/ui/confirm-dialog";
import { SortableTableHead } from "../components/SortableTableHead";
import { useTableSort } from "../lib/use-table-sort";
import { SelectField } from "../components/ui/select";
import { useEffect, useState } from "react";
import { Plus, Search, Filter, Pencil, Trash2 } from "lucide-react";
import { Badge } from "../components/ui/badge";
import { useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "../components/ui/dialog";
import {
  allPages,
  type SubscriberList,
  type CustomField,
  ErrorMessage,
  Field,
  tableClass,
  useSubscriberAction,
} from "../components/subscribers/shared";

type Rule = {
  all?: Rule[];
  any?: Rule[];
  field?: string;
  op?: string;
  value?: string | number | boolean;
};
type Segment = { id: number; name: string; rules: Rule | string };
type Campaign = { id: number; subject: string };
const initialRule = (): Rule => ({
  field: "status",
  op: "eq",
  value: "active",
});
const operations: Record<string, string> = {
  eq: "Equals",
  ne: "Does not equal",
  gt: "Greater than",
  gte: "At least",
  lt: "Less than",
  lte: "At most",
  is_set: "Is set",
  not_set: "Is not set",
  in: "Matches",
  not_in: "Does not match",
};

function RuleEditor({
  rule,
  onChange,
  fields,
  lists,
  campaigns,
  depth = 0,
}: {
  rule: Rule;
  onChange: (v: Rule) => void;
  fields: CustomField[];
  lists: SubscriberList[];
  campaigns: Campaign[];
  depth?: number;
}) {
  const group = rule.all || rule.any;
  if (group) {
    const mode = rule.all ? "all" : "any";
    const replace = (rows: Rule[]) =>
      onChange(rows.length ? { [mode]: rows } : initialRule());
    return (
      <div className="rounded-lg border bg-muted/20 p-4 space-y-3">
        <div className="flex gap-3 items-center">
          <span className="text-sm">Match</span>
          <SelectField
            aria-label="Group matching"
            className={" max-w-44"}
            value={mode}
            onValueChange={(value) => onChange({ [value]: group })}
            options={[
              { value: "all", label: "All conditions" },
              { value: "any", label: "Any condition" },
            ]}
          />
        </div>
        {group.map((child, i) => (
          <div key={i} className="flex gap-2 items-start">
            <div className="flex-1">
              <RuleEditor
                rule={child}
                onChange={(v) =>
                  replace(group.map((r, j) => (i === j ? v : r)))
                }
                fields={fields}
                lists={lists}
                campaigns={campaigns}
                depth={depth + 1}
              />
            </div>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => replace(group.filter((_, j) => i !== j))}
              aria-label={`Remove condition ${i + 1}`}
            >
              Remove
            </Button>
          </div>
        ))}
        <div className="flex gap-2">
          <Button
            variant="outline"
            size="sm"
            disabled={group.length >= 100}
            onClick={() => replace([...group, initialRule()])}
          >
            Add condition
          </Button>
          <Button
            variant="outline"
            size="sm"
            disabled={depth >= 19 || group.length >= 100}
            onClick={() => replace([...group, { any: [initialRule()] }])}
          >
            Add group
          </Button>
        </div>
      </div>
    );
  }
  const field = rule.field || "status";
  const membership = ["list", "opened", "clicked"].includes(field);
  const custom = fields.find((f) => `fields.${f.key}` === field);
  const numeric =
    field === "inactive" || membership || custom?.type === "number";
  const date =
    field === "created_at" ||
    field === "gdpr_consent" ||
    custom?.type === "date";
  const type = numeric
    ? "number"
    : custom?.type === "boolean"
      ? "boolean"
      : date
        ? "date"
        : "text";
  const ops = membership
    ? ["in", "not_in"]
    : field === "inactive"
      ? ["eq"]
      : ["eq", "ne", "gt", "gte", "lt", "lte", "is_set", "not_set"];
  const val = rule.value ?? "";
  const changeField = (next: string) => {
    const f = fields.find((v) => `fields.${v.key}` === next);
    onChange({
      field: next,
      op: ["list", "opened", "clicked"].includes(next) ? "in" : "eq",
      value:
        next === "status"
          ? "active"
          : next === "inactive"
            ? 4
            : f?.type === "boolean"
              ? true
              : "",
    });
  };
  const changeValue = (value: string) => {
    let next: string | number | boolean = value;
    if (numeric && value !== "") next = Number(value);
    else if (type === "boolean") next = value === "true";
    else if (date && !custom && value)
      next = new Date(`${value}T00:00:00Z`).getTime();
    onChange({ ...rule, value: next });
  };
  const dateValue =
    !custom && typeof val === "number"
      ? new Date(val).toISOString().slice(0, 10)
      : String(val);
  return (
    <div className="grid gap-3 rounded-lg border p-4 sm:grid-cols-3">
      <Field label="Field">
        <SelectField
          value={field}
          onValueChange={(value) => changeField(value)}
          options={[
            ...[
              ["email", "Email"],
              ["name", "Name"],
              ["status", "Subscriber status"],
              ["created_at", "Created date (UTC)"],
              ["gdpr_consent", "GDPR consent date (UTC)"],
              ["list", "List membership"],
              ["opened", "Opened campaign"],
              ["clicked", "Clicked campaign"],
              ["inactive", "Inactive for N campaigns"],
            ].map(([v, l]) => ({ value: v, label: l })),
            ...fields
              .filter((f) => f.is_filterable)
              .map((f) => ({
                value: `fields.${f.key}`,
                label: `${f.label} (${f.type})`,
              })),
          ]}
        />
      </Field>
      <Field label="Operation">
        <SelectField
          value={rule.op || "eq"}
          onValueChange={(value) => onChange({ ...rule, op: value })}
          options={ops.map((op) => ({ value: op, label: operations[op] }))}
        />
      </Field>
      {!["is_set", "not_set"].includes(rule.op || "") && (
        <Field label={field === "inactive" ? "Campaigns" : "Value"}>
          {membership ? (
            <SelectField
              value={String(val)}
              onValueChange={(value) => changeValue(value)}
              options={[
                {
                  value: "",
                  label: [
                    "Choose",
                    field === "list" ? "list" : "campaign",
                  ].join(" "),
                },
                ...(field === "list"
                  ? lists.map((l) => ({ id: l.id, label: l.name }))
                  : campaigns.map((c) => ({ id: c.id, label: c.subject }))
                ).map((v) => ({ value: String(v.id), label: v.label })),
              ]}
            />
          ) : field === "status" ? (
            <SelectField
              value={String(val)}
              onValueChange={(value) => changeValue(value)}
              options={["active", "bounced", "complained", "blocklisted"].map(
                (s) => ({ value: s, label: s }),
              )}
            />
          ) : type === "boolean" ? (
            <SelectField
              value={String(val)}
              onValueChange={(value) => changeValue(value)}
              options={[
                { value: "true", label: "True" },
                { value: "false", label: "False" },
              ]}
            />
          ) : (
            <Input
              type={type === "number" ? "number" : date ? "date" : "text"}
              min={numeric ? 1 : undefined}
              step={custom?.type === "number" ? "any" : undefined}
              value={date ? dateValue : String(val)}
              onChange={(e) => changeValue(e.target.value)}
            />
          )}
        </Field>
      )}
      {depth === 0 && (
        <Button
          className="sm:col-span-3 justify-self-start"
          variant="outline"
          size="sm"
          onClick={() => onChange({ all: [rule] })}
        >
          Add more conditions
        </Button>
      )}
    </div>
  );
}

function summarizeRule(rule: Rule): string {
  if (rule.all) return rule.all.map(summarizeRule).join(" and ");
  if (rule.any) return `(${rule.any.map(summarizeRule).join(" or ")})`;
  const field = (rule.field || "status")
    .replace("fields.", "")
    .replaceAll("_", " ");
  const op = operations[rule.op || "eq"] || rule.op || "Equals";
  return `${field} ${op.toLowerCase()}${["is_set", "not_set"].includes(rule.op || "") ? "" : ` ${String(rule.value ?? "")}`}`;
}
function segmentRuleSummary(rules: Rule | string): string {
  try {
    return summarizeRule(typeof rules === "string" ? JSON.parse(rules) : rules);
  } catch {
    return typeof rules === "string" ? rules : JSON.stringify(rules);
  }
}
function SegmentsWorkspace({ brandId }: { brandId: number }) {
  const sorting = useTableSort<"name" | "rules">("name");
  const [searchParams, setSearchParams] = useSearchParams();
  const [editing, setEditing] = useState<Segment | null>(null);
  const [search, setSearch] = useState("");
  const [name, setName] = useState("");
  const [rule, setRule] = useState<Rule>(initialRule());
  const [preview, setPreview] = useState<{
    key: string;
    count?: number;
    error?: unknown;
  }>({ key: "" });
  const [deleteRow, setDeleteRow] = useState<Segment | null>(null);
  const [localError, setLocalError] = useState<unknown>();
  const action = useSubscriberAction(brandId);
  const segments = useQuery({
    queryKey: ["subscribers", brandId, "segments"],
    queryFn: ({ signal }) => allPages<Segment>("/segments", brandId, signal),
    enabled: brandId > 0,
  });
  const fields = useQuery({
    queryKey: ["subscribers", brandId, "fields"],
    queryFn: ({ signal }) =>
      api<CustomField[]>("/custom-fields", { brandId, signal }),
    enabled: brandId > 0,
  });
  const lists = useQuery({
    queryKey: ["subscribers", brandId, "lists"],
    queryFn: ({ signal }) =>
      allPages<SubscriberList>("/lists", brandId, signal),
    enabled: brandId > 0,
  });
  const campaigns = useQuery({
    queryKey: ["subscribers", brandId, "segment-campaigns"],
    queryFn: ({ signal }) => allPages<Campaign>("/campaigns/options", brandId, signal),
    enabled: brandId > 0,
  });
  const visibleSegments = sorting.sort(
    (segments.data || []).filter((segment) =>
      segment.name.toLowerCase().includes(search.trim().toLowerCase()),
    ),
    (segment, key) =>
      key === "name" ? segment.name : segmentRuleSummary(segment.rules),
  );
  const previewKey = `${brandId}:${JSON.stringify(rule)}`;
  useEffect(() => {
    setEditing(null);
    setDeleteRow(null);
    setLocalError(undefined);
  }, [brandId]);
  useEffect(() => {
    if (!editing || !brandId) return;
    const abort = new AbortController();
    let live = true;
    const timer = setTimeout(() => {
      api<{ count: number }>("/segments/preview", {
        method: "POST",
        brandId,
        body: rule,
        signal: abort.signal,
      })
        .then((v) => {
          if (live) setPreview({ key: previewKey, count: v.count });
        })
        .catch((e) => {
          if (live && !abort.signal.aborted)
            setPreview({ key: previewKey, error: e });
        });
    }, 400);
    return () => {
      live = false;
      clearTimeout(timer);
      abort.abort();
    };
  }, [previewKey, editing, brandId, rule]);
  const edit = (s?: Segment) => {
    action.reset();
    setLocalError(undefined);
    setPreview({ key: "" });
    setEditing(s || { id: 0, name: "", rules: initialRule() });
    setName(s?.name || "");
    try {
      setRule(
        s
          ? typeof s.rules === "string"
            ? JSON.parse(s.rules)
            : s.rules
          : initialRule(),
      );
    } catch (e) {
      setLocalError(e);
    }
  };
  useEffect(() => {
    if (searchParams.get("new")) {
      edit();
      const next = new URLSearchParams(searchParams);
      next.delete("new");
      setSearchParams(next, { replace: true });
    }
  }, [searchParams, setSearchParams]);
  return (
    <div className="space-y-6">
      <div className="page-header flex flex-wrap items-end justify-between gap-2">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Segments</h1>
          <p className="page-subtitle text-sm text-muted-foreground mt-1">
            Build audiences from list memberships, engagement and indexed custom
            fields.
          </p>
        </div>
        <Button onClick={() => edit()}>
          <Plus size={16} />
          New segment
        </Button>
      </div>
      <ErrorMessage
        error={segments.error || fields.error || lists.error || campaigns.error}
      />
      <div className="table-card segments-table-card">
        <div className="table-toolbar toolbar flex flex-wrap items-center justify-between gap-3">
          <div className="relative w-full max-w-sm">
            <Search
              size={16}
              className="absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground"
            />
            <Input
              aria-label="Search segments"
              className="toolbar-search pl-9"
              placeholder="Search segments…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
          <Badge variant="outline">
            {segments.data?.length || 0} saved segments
          </Badge>
        </div>
        {segments.isPending ? (
          <p role="status">Loading segments…</p>
        ) : !segments.data?.length ? (
          <div className="empty-panel rounded-lg p-10 text-center text-muted-foreground">
            <Filter size={28} className="mx-auto mb-3" />
            <p className="row-title">Create your first segment</p>
            <p className="row-subtitle mt-1">
              Build rules to define an audience by membership, engagement or
              custom fields.
            </p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className={tableClass}>
              <thead>
                <tr>
                  <SortableTableHead label="Name" {...sorting.header("name")} />
                  <SortableTableHead label="Rules" {...sorting.header("rules")} />
                  <th>
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {!visibleSegments.length && (
                  <tr>
                    <td colSpan={3} className="empty-panel">
                      No segments match your search.
                    </td>
                  </tr>
                )}
                {visibleSegments.map((s) => (
                  <tr key={s.id}>
                    <td>
                      <div className="flex items-center gap-3">
                        <span className="avatar">
                          <Filter size={16} />
                        </span>
                        <span className="row-title">{s.name}</span>
                      </div>
                    </td>
                    <td>
                      <p className="text-sm max-w-xl">
                        {segmentRuleSummary(s.rules)}
                      </p>
                      <details className="mt-1 text-xs text-muted-foreground">
                        <summary className="cursor-pointer">
                          View rule JSON
                        </summary>
                        <code className="block max-w-xl break-all mt-2">
                          {typeof s.rules === "string"
                            ? s.rules
                            : JSON.stringify(s.rules)}
                        </code>
                      </details>
                    </td>
                    <td>
                      <div className="campaign-row-actions">
                        <Button
                          size="icon"
                          variant="ghost"
                          className="action-edit"
                          aria-label={`Edit segment ${s.name}`}
                          title="Edit segment"
                          onClick={() => edit(s)}
                        >
                          <Pencil size={16} aria-hidden="true" />
                        </Button>
                        <Button
                          size="icon"
                          variant="ghost"
                          className="campaign-delete-action"
                          aria-label={`Delete segment ${s.name}`}
                          title="Delete segment"
                          onClick={() => {
                            action.reset();
                            setDeleteRow(s);
                          }}
                        >
                          <Trash2 size={16} aria-hidden="true" />
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <div className="table-footer text-xs text-muted-foreground">
          {visibleSegments.length}{" "}
          {visibleSegments.length === 1 ? "segment" : "segments"} shown.
          Matching subscribers are counted when you edit a segment.
        </div>
      </div>
      <Dialog
        open={!!editing}
        onOpenChange={(open) => !open && setEditing(null)}
      >
        <DialogContent className="w-[calc(100vw-2rem)] max-w-xl max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>
              {editing?.id ? "Edit segment" : "New segment"}
            </DialogTitle>
          </DialogHeader>
          <Field label="Name">
            <Input value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <RuleEditor
            rule={rule}
            onChange={setRule}
            fields={fields.data || []}
            lists={lists.data || []}
            campaigns={campaigns.data || []}
          />
          <div aria-live="polite" className="rounded-lg bg-muted p-3 text-sm">
            {preview.key !== previewKey ? (
              "Counting matches…"
            ) : preview.error ? (
              <ErrorMessage error={preview.error} />
            ) : (
              `${preview.count?.toLocaleString()} matching subscribers. Sending also applies subscriber status and suppression checks.`
            )}
          </div>
          <ErrorMessage error={localError || action.error} />
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditing(null)}>
              Cancel
            </Button>
            <Button
              disabled={
                !name.trim() ||
                action.isPending ||
                preview.key !== previewKey ||
                !!preview.error
              }
              onClick={async () => {
                try {
                  await action.mutateAsync({
                    path: editing?.id ? `/segments/${editing.id}` : "/segments",
                    method: editing?.id ? "PATCH" : "POST",
                    body: { name: name.trim(), rules: rule },
                  });
                  setEditing(null);
                } catch {
                  /* displayed */
                }
              }}
            >
              Save segment
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        open={!!deleteRow}
        onOpenChange={(open) => !open && setDeleteRow(null)}
        title={`Delete ${deleteRow?.name}?`}
        description={
          <>
            This removes the saved segment and removes it from draft campaign
            audiences. Subscribers remain in the brand. Segments used by
            scheduled or active campaigns cannot be deleted.
          </>
        }
        error={action.error}
        pending={action.isPending}
        pendingLabel="Deleting…"
        confirmLabel="Delete segment"
        onConfirm={async () => {
          try {
            await action.mutateAsync({
              path: `/segments/${deleteRow?.id}`,
              method: "DELETE",
            });
            setDeleteRow(null);
          } catch {
            /* displayed */
          }
        }}
      />
    </div>
  );
}

export function SegmentsPage({ brandId }: { brandId: number }) {
  return brandId > 0 ? (
    <SegmentsWorkspace key={brandId} brandId={brandId} />
  ) : (
    <p>Select a brand to manage segments.</p>
  );
}
