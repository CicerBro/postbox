import { InputField } from "../components/ui/field";
import { useConfirmDialog } from "../components/ui/confirm-dialog";
import { SortableTableHead } from "../components/SortableTableHead";
import { useTableSort } from "../lib/use-table-sort";
import { useEffect, useState } from "react";
import { KeyRound, Plus, ShieldCheck } from "lucide-react";
import { Badge } from "../components/ui/badge";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useApp } from "../lib/app-context";
import { Button } from "../components/ui/button";
import { Checkbox } from "../components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../components/ui/dialog";
import {
  Section,
  Message,
  useAction,
  Secret,
  strings,
  date,
} from "../components/settings/Common";
type Key = {
  id: number;
  name: string;
  prefix: string;
  scopes: string | string[];
  last_used_at: number | null;
  revoked_at: number | null;
};
export function ApiKeysPage({
  brandId,
  embedded = false,
}: {
  brandId: number;
  embedded?: boolean;
}) {
  const sorting = useTableSort<"name" | "scopes" | "used" | "status">("name");
  const { session } = useApp();
  const qc = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [secret, setSecret] = useState("");
  const a = useAction();
  const { requestConfirmation, closeConfirmation, confirmationDialog } =
    useConfirmDialog({ pending: a.busy, error: a.error });
  useEffect(() => {
    setSecret("");
    setCreateOpen(false);
    closeConfirmation();
  }, [brandId]);
  const q = useQuery({
    queryKey: ["api-keys", brandId],
    queryFn: () => api<Key[]>("/api-keys", { brandId }),
    enabled: brandId > 0 && session.admin,
  });
  const keys = sorting.sort(q.data ?? [], (key, column) => {
    if (column === "name") return key.name;
    if (column === "scopes") return strings(key.scopes).join(", ");
    if (column === "used") return key.last_used_at;
    return key.revoked_at ? "Revoked" : "Active";
  });
  if (!brandId)
    return (
      <div className="empty-panel">
        <KeyRound size={28} aria-hidden="true" />
        <h2>Select a brand</h2>
        <p className="page-subtitle">
          Choose a brand to manage its integration keys.
        </p>
      </div>
    );
  if (!session.admin)
    return (
      <div className="empty-panel">
        <ShieldCheck size={28} aria-hidden="true" />
        <h2>Administrator access required</h2>
        <p className="page-subtitle">
          API key management requires an administrator session.
        </p>
      </div>
    );
  return (
    <div className="space-y-6">
      {!embedded && (
        <div className="page-heading">
          <div>
            <h1 className="text-2xl font-bold tracking-tight">API keys</h1>
            <p className="page-subtitle">
              Connect your tools with secure access to this brand.
            </p>
          </div>
          <Badge>
            <KeyRound size={13} aria-hidden="true" />
            Brand scoped
          </Badge>
        </div>
      )}
      <Section
        title="Brand API keys"
        description="Store secrets securely in your integration. Revoking a key takes effect immediately."
        action={
          <Button onClick={() => setCreateOpen(true)} disabled={a.busy}>
            <Plus size={16} aria-hidden="true" />
            Create API key
          </Button>
        }
      >
        <Message {...a} />
        {secret && <Secret value={secret} onDismiss={() => setSecret("")} />}
        <Message error={q.error} />
        {q.isPending ? (
          <p role="status">Loading keys…</p>
        ) : q.isError ? (
          <Button variant="outline" onClick={() => q.refetch()}>
            Retry
          </Button>
        ) : !q.data?.length ? (
          <div className="empty-panel">
            <KeyRound size={26} aria-hidden="true" />
            <h3>No API keys yet</h3>
            <p className="page-subtitle">
              Create an API key to connect your first integration.
            </p>
          </div>
        ) : (
          <div className="table-card relative overflow-x-auto">
            <table className="w-full text-left text-sm">
              <thead>
                <tr>
                  <SortableTableHead label="Name" {...sorting.header("name")} />
                  <th>Prefix</th>
                  <SortableTableHead
                    label="Scopes"
                    {...sorting.header("scopes")}
                  />
                  <SortableTableHead
                    label="Last used"
                    {...sorting.header("used")}
                  />
                  <SortableTableHead
                    label="Status"
                    {...sorting.header("status")}
                  />
                  <th>
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {keys.map((k) => (
                  <tr className="border-t" key={k.id}>
                    <td className="p-3">
                      <span className="row-title">{k.name}</span>
                    </td>
                    <td className="p-3">
                      <code className="row-subtitle">{k.prefix}</code>
                    </td>
                    <td className="p-3">
                      <div className="flex flex-wrap gap-1 capitalize">
                        {strings(k.scopes).map((scope) => (
                          <Badge key={scope}>
                            {scope.charAt(0).toUpperCase() + scope.slice(1)}
                          </Badge>
                        ))}
                      </div>
                    </td>
                    <td className="p-3">{date(k.last_used_at)}</td>
                    <td className="p-3">
                      <Badge variant={k.revoked_at ? "secondary" : "success"}>
                        <span className="status-dot" />
                        {k.revoked_at ? "Revoked" : "Active"}
                      </Badge>
                    </td>
                    <td className="p-3">
                      {!k.revoked_at && (
                        <Button
                          variant="destructive"
                          size="sm"
                          disabled={a.busy}
                          onClick={() => {
                            requestConfirmation({
                              title: "Revoke key?",
                              description: `Revoke ${k.name}? Integrations using it will stop working.`,
                              confirmLabel: "Revoke key",
                              onConfirm: () =>
                                a.run(async () => {
                                  await api(`/api-keys/${k.id}`, {
                                    method: "DELETE",
                                    brandId,
                                  });
                                  await qc.invalidateQueries({
                                    queryKey: ["api-keys", brandId],
                                  });

                                  closeConfirmation();
                                }, "Key revoked."),
                            });
                          }}
                        >
                          Revoke
                        </Button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Section>
      {createOpen && (
        <CreateKeyDialog
          brandId={brandId}
          onClose={() => setCreateOpen(false)}
          onCreated={(value) => {
            setSecret(value);
            setCreateOpen(false);
            void qc.invalidateQueries({ queryKey: ["api-keys", brandId] });
          }}
        />
      )}
      {confirmationDialog}
    </div>
  );
}

function CreateKeyDialog({
  brandId,
  onClose,
  onCreated,
}: {
  brandId: number;
  onClose: () => void;
  onCreated: (secret: string) => void;
}) {
  const [name, setName] = useState("");
  const [scopes, setScopes] = useState(["read"]);
  const create = useMutation({
    mutationFn: () =>
      api<{ secret: string }>("/api-keys", {
        method: "POST",
        brandId,
        body: { name, scopes },
      }),
    onSuccess: (result) => onCreated(result.secret),
  });
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !create.isPending) onClose();
      }}
    >
      <DialogContent
        width="md"
        showCloseButton={!create.isPending}
        aria-busy={create.isPending}
        onOverlayClick={() => {
          if (!create.isPending) onClose();
        }}
      >
        <DialogHeader>
          <DialogTitle>Create API key</DialogTitle>
          <DialogDescription>
            Give each integration its own key and choose the access it needs.
          </DialogDescription>
        </DialogHeader>
        <form
          className="space-y-5"
          onSubmit={(event) => {
            event.preventDefault();
            if (scopes.length && !create.isPending) create.mutate();
          }}
        >
          <InputField
            label="Key name"
            placeholder="e.g. Website integration"
            value={name}
            required
            disabled={create.isPending}
            onChange={(event) => setName(event.target.value)}
          />
          <fieldset disabled={create.isPending}>
            <legend className="mb-3 text-sm font-medium">Permissions</legend>
            <div className="grid gap-3 sm:grid-cols-2">
              {(
                [
                  ["read", "Read", "Retrieve brand data."],
                  ["write", "Write", "Make changes to this brand."],
                ] as const
              ).map(([scope, label, description]) => (
                <label key={scope} className="rounded-lg border p-3">
                  <span className="flex items-center gap-2 text-sm font-medium">
                    <Checkbox
                      checked={scopes.includes(scope)}
                      disabled={create.isPending}
                      onCheckedChange={(checked) =>
                        setScopes((current) =>
                          checked === true
                            ? [...current, scope]
                            : current.filter((value) => value !== scope),
                        )
                      }
                    />
                    {label}
                  </span>
                  <span className="mt-1 ml-6 block text-xs text-muted-foreground">
                    {description}
                  </span>
                </label>
              ))}
            </div>
          </fieldset>
          <Message error={create.error} />
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={create.isPending}
              onClick={onClose}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending || !scopes.length}>
              {create.isPending ? "Creating…" : "Create key"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
