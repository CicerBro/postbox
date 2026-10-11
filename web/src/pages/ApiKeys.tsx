import { SortableTableHead } from "../components/SortableTableHead";
import { useTableSort } from "../lib/use-table-sort";
import { useEffect, useState } from "react";
import { KeyRound, Plus, ShieldCheck } from "lucide-react";
import { Badge } from "../components/ui/badge";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useApp } from "../lib/app-context";
import { Button } from "../components/ui/button";
import {
  Section,
  Field,
  Message,
  useAction,
  Secret,
  Checks,
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
export function ApiKeysPage({ brandId }: { brandId: number }) {
  const sorting = useTableSort<"name" | "scopes" | "used" | "status">("name");
  const { session } = useApp();
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [scopes, setScopes] = useState(["read"]);
  const [secret, setSecret] = useState("");
  const a = useAction();
  useEffect(() => {
    setSecret("");
    setName("");
    setScopes(["read"]);
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
      <Section
        title="Create API key"
        description="Give each integration its own key and choose the access it needs."
      >
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            void a.run(async () => {
              const r = await api<{ secret: string }>("/api-keys", {
                method: "POST",
                brandId,
                body: { name, scopes },
              });
              setSecret(r.secret);
              setName("");
              await qc.invalidateQueries({ queryKey: ["api-keys", brandId] });
            }, "API key created.");
          }}
        >
          <Field
            label="Key name"
            placeholder="e.g. Website integration"
            value={name}
            required
            onChange={(e) => setName(e.target.value)}
          />
          <div className="space-y-2">
            <p className="text-sm font-medium">Permissions</p>
            <Checks
              values={["read", "write"]}
              selected={scopes}
              onChange={setScopes}
            />
            <p className="text-xs text-muted-foreground">
              Read retrieves brand data. Write allows changes.
            </p>
          </div>
          <Button disabled={a.busy || !scopes.length}>
            <Plus size={16} aria-hidden="true" />
            Create key
          </Button>
        </form>
        <Message {...a} />
        {secret && <Secret value={secret} onDismiss={() => setSecret("")} />}
      </Section>
      <Section
        title="Brand API keys"
        description="Store secrets securely in your integration. Revoking a key takes effect immediately."
      >
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
              Create a key above to connect your first integration.
            </p>
          </div>
        ) : (
          <div className="table-card overflow-x-auto">
            <table className="w-full text-left text-sm">
              <thead>
                <tr>
                  <SortableTableHead label="Name / prefix" {...sorting.header("name")} />
                  <th>Prefix</th>
                  <SortableTableHead label="Scopes" {...sorting.header("scopes")} />
                  <SortableTableHead label="Last used" {...sorting.header("used")} />
                  <SortableTableHead label="Status" {...sorting.header("status")} />
                  <th>
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {keys.map((k) => (
                  <tr className="border-t" key={k.id}>
                    <td className="p-3"><span className="row-title">{k.name}</span></td>
                    <td className="p-3"><code className="row-subtitle">{k.prefix}</code></td>
                    <td className="p-3">
                      <div className="flex flex-wrap gap-1">
                        {strings(k.scopes).map((scope) => (
                          <Badge key={scope}>{scope}</Badge>
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
                            if (
                              window.confirm(
                                `Revoke ${k.name}? Integrations using it will stop working.`,
                              )
                            )
                              void a.run(async () => {
                                await api(`/api-keys/${k.id}`, {
                                  method: "DELETE",
                                  brandId,
                                });
                                await qc.invalidateQueries({
                                  queryKey: ["api-keys", brandId],
                                });
                              }, "Key revoked.");
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
    </div>
  );
}
