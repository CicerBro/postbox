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
  ErrorMessage,
  initialHTML,
  tableClass,
  Template,
} from "./campaign-shared";
export function TemplatesPage({ brandId }: { brandId: number }) {
  return <TemplateLibrary key={brandId} brandId={brandId} />;
}
function TemplateLibrary({ brandId }: { brandId: number }) {
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
    [notice, setNotice] = useState("");
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
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Templates</h1>
          <p className="text-sm text-muted-foreground">
            Reusable full HTML documents for this brand.
          </p>
        </div>
        <Button onClick={() => edit(null)}>New template</Button>
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
        <div className="overflow-auto rounded-lg border">
          <table className={tableClass}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Updated</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {query.data?.map((t) => (
                <tr key={t.id}>
                  <td className="font-medium">{t.name}</td>
                  <td>{new Date(t.updated_at).toLocaleString()}</td>
                  <td>
                    <div className="flex gap-2">
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => edit(t)}
                      >
                        Edit
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => {
                          setError(null);
                          setDeleting(t);
                        }}
                      >
                        Delete
                      </Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {!query.data?.length && (
            <p className="p-6 text-sm text-muted-foreground">
              No templates on this page. Create one or paste an existing full
              HTML document.
            </p>
          )}
        </div>
      )}
      <div className="flex gap-2">
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
