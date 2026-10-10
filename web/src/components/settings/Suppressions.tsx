import { useState } from "react";
import { Badge } from "../ui/badge";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { useApp } from "../../lib/app-context";
import { Button } from "../ui/button";
import { SelectField } from "../ui/select";
import { Section, Field, Message, useAction } from "./Common";
type Suppression = {
  id: number;
  brand_id: number | null;
  kind: string;
  value: string;
  reason: string;
};
export function Suppressions({ brandId }: { brandId: number }) {
  const { session } = useApp();
  const qc = useQueryClient();
  const [scope, setScope] = useState("effective");
  const [target, setTarget] = useState("brand");
  const [kind, setKind] = useState("email");
  const [value, setValue] = useState("");
  const a = useAction();
  const q = useInfiniteQuery({
    queryKey: ["suppressions", brandId, scope],
    initialPageParam: 0,
    queryFn: ({ pageParam }) =>
      api<Suppression[]>(
        `/suppressions?scope=${scope}&after=${pageParam}&limit=100`,
        scope === "global" ? {} : { brandId },
      ),
    getNextPageParam: (rows) =>
      rows.length === 100 ? rows.at(-1)?.id : undefined,
  });
  return (
    <Section title="Suppressions">
      <p className="text-sm text-muted-foreground">
        Effective suppressions include brand and global entries. Removing an
        entry does not reactivate bounced or complained subscribers.
      </p>
      <label htmlFor="suppression-scope" className="block text-sm space-y-2">
        Show
        <SelectField
          id="suppression-scope"
          value={scope}
          onValueChange={setScope}
          options={[
            { value: "effective", label: "Effective: brand and global" },
            { value: "brand", label: "Brand only" },
            ...(session.admin ? [{ value: "global", label: "Global only" }] : []),
          ]}
        />
      </label>
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          void a.run(async () => {
            await api(`/suppressions?scope=${target}`, {
              method: "POST",
              ...(target === "global" ? {} : { brandId }),
              body: { kind, value },
            });
            setValue("");
            await qc.invalidateQueries({ queryKey: ["suppressions"] });
          }, "Suppression added.");
        }}
      >
        <div className="flex flex-wrap gap-3">
          <label htmlFor="suppression-kind" className="text-sm space-y-2">
            Kind
            <SelectField
              id="suppression-kind"
              value={kind}
              onValueChange={setKind}
              options={[
                { value: "email", label: "Email" },
                { value: "domain", label: "Domain" },
              ]}
            />
          </label>
          {session.admin && (
            <label htmlFor="suppression-target" className="text-sm space-y-2">
              Scope
              <SelectField
                id="suppression-target"
                value={target}
                onValueChange={setTarget}
                options={[
                  { value: "brand", label: "Selected brand" },
                  { value: "global", label: "All brands" },
                ]}
              />
            </label>
          )}
        </div>
        <Field
          label={kind === "email" ? "Email address" : "Domain"}
          type={kind === "email" ? "email" : "text"}
          required
          value={value}
          onChange={(e) => setValue(e.target.value)}
        />
        <Button disabled={a.busy}>Add suppression</Button>
      </form>
      <Message {...a} />
      <Message error={q.error} />
      {q.isPending ? (
        <p role="status">Loading suppressions…</p>
      ) : q.isError ? (
        <Button variant="outline" onClick={() => q.refetch()}>
          Retry
        </Button>
      ) : !q.data?.pages.flat().length ? (
        <div className="empty-panel">
          <h3>No suppressions in this scope</h3>
          <p className="page-subtitle">
            Suppressed addresses and domains will appear here.
          </p>
        </div>
      ) : (
        <div className="table-card overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead>
              <tr>
                <th className="p-3">Value</th>
                <th>Kind</th>
                <th>Scope</th>
                <th>Reason</th>
                <th>
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {q.data.pages.flat().map((s) => (
                <tr key={s.id} className="border-t">
                  <td className="p-3">
                    <span className="row-title">{s.value}</span>
                  </td>
                  <td>
                    <Badge>{s.kind}</Badge>
                  </td>
                  <td>
                    <Badge variant={s.brand_id === null ? "info" : "secondary"}>
                      {s.brand_id === null ? "Global" : "Brand"}
                    </Badge>
                  </td>
                  <td>{s.reason}</td>
                  <td>
                    {(s.brand_id !== null || session.admin) && (
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={a.busy}
                        onClick={() => {
                          if (
                            window.confirm(
                              `Remove suppression for ${s.value}? Existing subscriber statuses will remain unchanged.`,
                            )
                          )
                            void a.run(async () => {
                              const global = s.brand_id === null;
                              await api(
                                `/suppressions/${s.id}?scope=${global ? "global" : "brand"}`,
                                {
                                  method: "DELETE",
                                  ...(global ? {} : { brandId }),
                                },
                              );
                              await qc.invalidateQueries({
                                queryKey: ["suppressions"],
                              });
                            }, "Suppression removed.");
                        }}
                      >
                        Remove
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {q.hasNextPage && (
        <Button
          variant="outline"
          disabled={q.isFetchingNextPage}
          onClick={() => q.fetchNextPage()}
        >
          Load more
        </Button>
      )}
    </Section>
  );
}
