import { useEffect, useState } from "react";
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
  if (!brandId) return <p>Select a brand to manage API keys.</p>;
  if (!session.admin)
    return <p>API key management requires an administrator session.</p>;
  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-semibold">API keys</h1>
      <p className="text-sm text-muted-foreground">
        Keys are limited to the selected brand. Store secrets securely in your
        integration.
      </p>
      <Section title="Create API key">
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
            label="Name"
            value={name}
            required
            onChange={(e) => setName(e.target.value)}
          />
          <Checks
            values={["read", "write"]}
            selected={scopes}
            onChange={setScopes}
          />
          <Button disabled={a.busy || !scopes.length}>Create key</Button>
        </form>
        <Message {...a} />
        {secret && <Secret value={secret} onDismiss={() => setSecret("")} />}
      </Section>
      <Section title="Brand API keys">
        <Message error={q.error} />
        {q.isPending ? (
          <p role="status">Loading keys…</p>
        ) : q.isError ? (
          <Button variant="outline" onClick={() => q.refetch()}>
            Retry
          </Button>
        ) : !q.data?.length ? (
          <p>No API keys yet.</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left text-sm">
              <thead>
                <tr>
                  <th className="p-3">Name / prefix</th>
                  <th>Scopes</th>
                  <th>Last used</th>
                  <th>Status</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {q.data.map((k) => (
                  <tr className="border-t" key={k.id}>
                    <td className="p-3">
                      {k.name}
                      <code className="block text-xs">{k.prefix}</code>
                    </td>
                    <td>{strings(k.scopes).join(", ")}</td>
                    <td>{date(k.last_used_at)}</td>
                    <td>{k.revoked_at ? "Revoked" : "Active"}</td>
                    <td>
                      {!k.revoked_at && (
                        <Button
                          variant="outline"
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
