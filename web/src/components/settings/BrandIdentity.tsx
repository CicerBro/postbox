import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { useApp } from "../../lib/app-context";
import { Button } from "../ui/button";
import { Section, Field, Message, useAction } from "./Common";
type Identity = {
  name: string;
  from_name: string;
  from_email: string;
  reply_to: string | null;
  configuration_set: string | null;
  tracking_domain: string | null;
};
const blank: Identity = {
  name: "",
  from_name: "",
  from_email: "",
  reply_to: "",
  configuration_set: "",
  tracking_domain: "",
};
export function BrandIdentity({ brandId }: { brandId: number }) {
  const { session } = useApp();
  const qc = useQueryClient();
  const [form, setForm] = useState(blank);
  const a = useAction();
  const q = useQuery({
    queryKey: ["brand-settings", brandId],
    queryFn: () => api<Identity[]>(`/brands/${brandId}`, { brandId }),
    enabled: brandId > 0,
  });
  useEffect(() => {
    if (q.data?.[0]) setForm(q.data[0]);
  }, [q.data]);
  return (
    <Section
      title="Brand identity"
      description="Sender details, Amazon SES configuration and tracking for this brand."
    >
      <Message error={q.error} />
      {q.isPending ? (
        <p role="status">Loading brand…</p>
      ) : q.isError ? (
        <Button variant="outline" onClick={() => q.refetch()}>
          Retry
        </Button>
      ) : q.data?.[0] ? (
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            void a.run(async () => {
              await api(`/brands/${brandId}`, {
                method: "PATCH",
                brandId,
                body: Object.fromEntries(
                  Object.entries(form)
                    .filter(([k]) => k in blank)
                    .map(([k, v]) => [k, v ?? ""]),
                ),
              });
              await qc.invalidateQueries({
                queryKey: ["brand-settings", brandId],
              });
              await qc.invalidateQueries({ queryKey: ["brands"] });
            });
          }}
        >
          <div className="grid gap-4 sm:grid-cols-2">
            {Object.entries({
              name: "Brand name",
              from_name: "Sender name",
              from_email: "Sender email",
              reply_to: "Reply-to email",
              configuration_set: "SES configuration set",
              tracking_domain: "Tracking hostname",
            }).map(([key, label]) => (
              <Field
                key={key}
                label={label}
                type={
                  key.includes("email") || key === "reply_to" ? "email" : "text"
                }
                required={key === "name" || key === "from_email"}
                value={form[key as keyof Identity] ?? ""}
                disabled={!session.admin}
                onChange={(e) => setForm({ ...form, [key]: e.target.value })}
              />
            ))}
          </div>
          {session.admin ? (
            <Button disabled={a.busy}>Save identity</Button>
          ) : (
            <p className="text-sm text-muted-foreground">
              Only administrators can edit brand identity.
            </p>
          )}
          <Message {...a} />
        </form>
      ) : null}
    </Section>
  );
}
