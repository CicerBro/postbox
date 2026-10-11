import { SortableTableHead } from "../components/SortableTableHead";
import { useTableSort } from "../lib/use-table-sort";
import { Plus, Search, FileCode2, Pencil, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
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
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "../components/ui/card";
import { EmailEditor } from "../components/EmailEditor";
import {
  contentExcerpt,
  bytes,
  ErrorMessage,
  initialHTML,
  tableClass,
  Template,
} from "./campaign-shared";
export function TemplatesPage({ brandId }: { brandId: number }) {
  return <TemplateLibrary key={brandId} brandId={brandId} />;
}
function TemplateLibrary({ brandId }: { brandId: number }) {
  const sorting = useTableSort<"name" | "size" | "updated">("name");
  const [params, setParams] = useSearchParams();
  const client = useQueryClient(),
    [after, setAfter] = useState(0),
    [history, setHistory] = useState<number[]>([]),
    [editing, setEditing] = useState<Template | null | undefined>(undefined),
    [name, setName] = useState(""),
    [html, setHtml] = useState(initialHTML),
    [deleting, setDeleting] = useState<Template | null>(null),
    [busy, setBusy] = useState(false),
    [error, setError] = useState<unknown>(null),
    [notice, setNotice] = useState(""),
    [search, setSearch] = useState("");
  useEffect(() => {
    if (params.get("new") === "1") {
      setEditing(null);
      setError(null);
      const next = new URLSearchParams(params);
      next.delete("new");
      setParams(next, { replace: true });
    }
  }, [params, setParams]);
  const query = useQuery({
    queryKey: ["templates", brandId, after],
    queryFn: () =>
      api<Template[]>(`/templates?after=${after}&limit=50`, { brandId }),
    enabled: brandId > 0,
  });
  useEffect(() => {
    if (editing !== undefined) {
      setName(editing?.name ?? "");
      setHtml(editing?.html ?? initialHTML);
    }
  }, [editing]);
  const visibleTemplates = sorting.sort(
    (query.data ?? []).filter((t) =>
      t.name.toLowerCase().includes(search.toLowerCase()),
    ),
    (t, key) =>
      key === "name"
        ? t.name
        : key === "size"
          ? new TextEncoder().encode(t.html).length
          : t.updated_at,
  );
  function edit(template: Template | null) {
    setError(null);
    setNotice("");
    setEditing(template);
  }
  async function save() {
    setBusy(true);
    setError(null);
    try {
      if (!name.trim() || !html.trim())
        throw new Error("Template name and HTML are required.");
      await api(editing ? `/templates/${editing.id}` : "/templates", {
        method: editing ? "PATCH" : "POST",
        brandId,
        body: { name, html },
      });
      setEditing(undefined);
      setNotice("Template saved.");
      await client.invalidateQueries({ queryKey: ["templates", brandId] });
      client.invalidateQueries({ queryKey: ["campaign-choices", brandId] });
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }
  async function remove() {
    if (!deleting) return;
    setBusy(true);
    setError(null);
    try {
      await api(`/templates/${deleting.id}`, { method: "DELETE", brandId });
      setDeleting(null);
      setNotice("Template deleted.");
      await client.invalidateQueries({ queryKey: ["templates", brandId] });
      client.invalidateQueries({ queryKey: ["campaign-choices", brandId] });
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="space-y-6">
      <div className="page-header flex flex-wrap items-end justify-between gap-2">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Templates</h1>
          <p className="page-subtitle text-sm text-muted-foreground">
            Reusable full HTML documents for this brand.
          </p>
        </div>
        <Button onClick={() => edit(null)}>
          <Plus size={16} /> New template
        </Button>
      </div>
      <ErrorMessage error={query.error ?? error} />
      {notice && (
        <p role="status" className="rounded-md border p-3 text-sm">
          {notice}
        </p>
      )}
      {editing !== undefined && (
        <Card>
          <CardHeader>
            <CardTitle>{editing ? "Edit template" : "New template"}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <label className="block text-sm">
              Name
              <Input value={name} onChange={(e) => setName(e.target.value)} />
            </label>
            <EmailEditor brandId={brandId} value={html} onChange={setHtml} />
            <div className="flex gap-2">
              <Button disabled={busy} onClick={save}>
                {busy ? "Saving…" : "Save template"}
              </Button>
              <Button
                variant="outline"
                disabled={busy}
                onClick={() => setEditing(undefined)}
              >
                Close editor
              </Button>
            </div>
          </CardContent>
        </Card>
      )}
      {query.isPending ? (
        <p role="status">Loading templates…</p>
      ) : (
        <div className="table-card">
          <div className="table-toolbar">
            <div>
              <h2 className="section-heading">Template library</h2>
              <p className="row-subtitle">
                {query.data?.length ?? 0} designs on this page. Sorting applies
                to this page.
              </p>
            </div>
            <div className="toolbar-search">
              <Search size={16} />
              <Input
                aria-label="Search templates on this page"
                placeholder="Search this page…"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
            </div>
          </div>
          <div className="overflow-auto">
            <table className={tableClass}>
              <thead>
                <tr>
                  <SortableTableHead label="Template" {...sorting.header("name")} />
                  <SortableTableHead label="HTML size" {...sorting.header("size")} />
                  <SortableTableHead label="Updated" {...sorting.header("updated")} />
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {visibleTemplates.map((t) => (
                    <tr key={t.id}>
                      <td>
                        <div className="flex items-center gap-3">
                          <span className="avatar">
                            <FileCode2 size={18} />
                          </span>
                          <div>
                            <button
                              type="button"
                              className="row-title text-left font-medium hover:underline"
                              onClick={() => edit(t)}
                            >
                              {t.name}
                            </button>
                            <p className="row-subtitle max-w-sm truncate">
                              {contentExcerpt(t.html) || "HTML email template"}
                            </p>
                          </div>
                        </div>
                      </td>
                      <td className="text-muted-foreground">
                        {bytes(new TextEncoder().encode(t.html).length)}
                      </td>
                      <td>{new Date(t.updated_at).toLocaleString()}</td>
                      <td>
                        <div className="flex gap-2">
                          <Button
                            variant="outline"
                            size="icon"
                            className="action-edit"
                            aria-label={`Edit template ${t.name}`}
                            title="Edit template"
                            onClick={() => edit(t)}
                          >
                            <Pencil size={16} aria-hidden="true" />
                          </Button>
                          <Button
                            variant="outline"
                            size="icon"
                            className="action-delete"
                            aria-label={`Delete template ${t.name}`}
                            title="Delete template"
                            onClick={() => {
                              setError(null);
                              setDeleting(t);
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
            {!query.data?.filter((t) =>
              t.name.toLowerCase().includes(search.toLowerCase()),
            ).length && (
              <div className="empty-panel">
                <FileCode2 size={28} />
                <h3>
                  {search
                    ? "No matching templates"
                    : "Your next campaign starts here"}
                </h3>
                <p>
                  {search
                    ? "Try another search on this page."
                    : "Create a reusable design or paste your existing HTML document."}
                </p>
              </div>
            )}
          </div>
        </div>
      )}
      <div className="table-footer flex gap-2">
        <span className="row-subtitle mr-auto">Page {history.length + 1}</span>
        <Button
          variant="outline"
          disabled={!history.length}
          onClick={() => {
            setAfter(history.at(-1) ?? 0);
            setHistory(history.slice(0, -1));
          }}
        >
          Previous
        </Button>
        <Button
          variant="outline"
          disabled={query.data?.length !== 50}
          onClick={() => {
            setHistory([...history, after]);
            setAfter(query.data!.at(-1)!.id);
          }}
        >
          Next
        </Button>
      </div>
      <Dialog
        open={deleting !== null}
        onOpenChange={(open) => {
          if (!open && !busy) setDeleting(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete template?</DialogTitle>
          </DialogHeader>
          <p className="text-sm">
            Permanently delete “{deleting?.name}”. Campaigns keep their own
            saved HTML.
          </p>
          <ErrorMessage error={error} />
          <DialogFooter>
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => setDeleting(null)}
            >
              Cancel
            </Button>
            <Button variant="destructive" disabled={busy} onClick={remove}>
              {busy ? "Deleting…" : "Delete template"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
