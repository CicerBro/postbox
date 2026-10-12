import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { Button } from "../ui/button";
import { ChevronDown } from "lucide-react";
import { Switch } from "../ui/switch";
import { EmailEditor } from "../EmailEditor";
import { Textarea } from "../ui/textarea";
import { Section, Field, Message, useAction } from "./Common";
type Email = { subject: string; html: string; text?: string };
type Settings = {
  thankyou_html?: string;
  goodbye_html?: string;
  confirmation_email?: Email;
  thankyou_email?: Email;
  goodbye_email?: Email;
};
const messageKeys = ["thankyou_html", "goodbye_html", "confirmation_email", "thankyou_email", "goodbye_email"] as const;

export function Messages({ brandId }: { brandId: number }) {
  const qc = useQueryClient();
  const [form, setForm] = useState<Settings>({});
  const [enabled, setEnabled] = useState<string[]>([]);
  const [expanded, setExpanded] = useState<string[]>([]);
  const a = useAction();
  const q = useQuery({
    queryKey: ["settings", brandId],
    queryFn: () => api<Settings>("/settings", { brandId }),
  });
  useEffect(() => {
    if (q.data) {
      setForm(q.data);
      setEnabled(Object.keys(q.data));
    }
  }, [q.data]);
  return (
    <Section title="Subscription pages and emails">
      <p className="text-sm text-muted-foreground">
        Brand defaults for hosted subscription flows. List-specific form
        settings take precedence. Switch on a custom page or email, expand it to
        edit, then save your changes. Saving an item switched off clears its custom
        content. Emails require a subject and HTML.
      </p>
      <Message error={q.error} />
      {q.isPending ? (
        <p role="status">Loading message settings…</p>
      ) : q.isError ? (
        <Button variant="outline" onClick={() => q.refetch()}>
          Retry
        </Button>
      ) : (
        <form
          className="space-y-5"
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            void a.run(async () => {
              for (const k of enabled) {
                const value = form[k as keyof Settings];
                if (k.endsWith("_email") &&
                    (!(value as Email)?.subject?.trim() || !(value as Email)?.html?.trim())) {
                  setExpanded((current) => current.includes(k) ? current : [...current, k]);
                  throw new Error("Enabled emails require a subject and HTML.");
                }
              }
              await api("/settings", {
                method: "PATCH",
                brandId,
                body: Object.fromEntries(
                  messageKeys.map((k) => [k, enabled.includes(k) ? form[k] ?? "" : null]),
                ),
              });
              await qc.invalidateQueries({ queryKey: ["settings", brandId] });
            });
          }}
        >
          {messageKeys.map((k) => {
            const labels = {
              thankyou_html: "Subscription thank-you page",
              goodbye_html: "Unsubscribe confirmation page",
              confirmation_email: "Double opt-in email",
              thankyou_email: "Welcome email",
              goodbye_email: "Goodbye email",
            };
            const active = enabled.includes(k);
            const value = form[k];
            return (
              <div
                key={k}
                className="rounded-xl border bg-muted/20 p-5 space-y-3"
              >
                <div className="flex items-center justify-between gap-4">
                  <div className="flex shrink-0 items-center gap-2">
                    <Switch aria-label={`Enable ${labels[k]}`} checked={active} disabled={a.busy}
                      onCheckedChange={(checked) => {
                        setEnabled((current) => checked ? [...current, k] : current.filter((key) => key !== k));
                        if (checked && k.endsWith("_email") && !form[k])
                          setForm((current) => ({ ...current, [k]: { subject: "", html: "", text: "" } }));
                      }} />
                    <span className="text-sm text-muted-foreground">{active ? "On" : "Off"}</span>
                  </div>
                  <Button type="button" variant="ghost" className="min-w-0 flex-1 justify-start whitespace-normal text-left"
                    aria-expanded={expanded.includes(k)} aria-controls={`message-content-${k}`}
                    onClick={() => setExpanded((current) => current.includes(k)
                      ? current.filter((key) => key !== k) : [...current, k])}>
                    <ChevronDown size={16} aria-hidden="true" className={`shrink-0 transition-transform ${expanded.includes(k) ? "rotate-180" : ""}`} />
                    {labels[k]}
                  </Button>
                </div>
                <p className="text-sm text-muted-foreground">
                  {k === "thankyou_email" || k === "goodbye_email"
                    ? "Off stops this brand email. List-specific settings can still enable it."
                    : "Off uses the built-in version. List-specific settings take precedence."}
                </p>
                <div id={`message-content-${k}`} hidden={!expanded.includes(k)}>
                  {expanded.includes(k) && <div className="space-y-4 pt-2">
                    {k.endsWith("_email") && <Field label="Subject" required disabled={!active || a.busy}
                      value={(value as Email)?.subject ?? ""}
                      onChange={(e) => setForm({ ...form, [k]: { ...(value as Email), subject: e.target.value } })} />}
                    <EmailEditor brandId={brandId}
                      value={typeof value === "string" ? value : value?.html ?? ""}
                      onChange={(html) => setForm((current) => ({ ...current,
                        [k]: k.endsWith("_html") ? html : { ...(current[k] as Email), html } }))}
                      readOnly={!active || a.busy}
                      subject={k.endsWith("_email") ? (value as Email)?.subject ?? "" : ""}
                      text={k.endsWith("_email") ? (value as Email)?.text ?? "" : ""}
                      previewMode={k.endsWith("_html") || k === "confirmation_email" ? "source" : "rendered"}
                      mergeTags={k.endsWith("_html") ? [] : k === "confirmation_email" ? ["confirmation_url"] : undefined} />
                    {k.endsWith("_email") && <label className="block space-y-2 text-sm">
                      Plain text (optional)
                      <Textarea rows={3} disabled={!active || a.busy} value={(value as Email)?.text ?? ""}
                        onChange={(e) => setForm({ ...form, [k]: { ...(value as Email), text: e.target.value } })} />
                    </label>}
                  </div>}
                </div>
              </div>
            );
          })}
          <Button disabled={a.busy}>Save messages</Button>
          <Message {...a} />
        </form>
      )}
    </Section>
  );
}
