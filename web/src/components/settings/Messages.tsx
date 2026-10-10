import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { Button } from "../ui/button";
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
export function Messages({ brandId }: { brandId: number }) {
  const qc = useQueryClient();
  const [form, setForm] = useState<Settings>({});
  const [enabled, setEnabled] = useState<string[]>([]);
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
        settings take precedence. Enable a field to configure it; existing email
        settings require a subject and HTML.
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
          onSubmit={(e) => {
            e.preventDefault();
            void a.run(async () => {
              await api("/settings", {
                method: "PATCH",
                brandId,
                body: Object.fromEntries(
                  enabled.map((k) => [k, form[k as keyof Settings] ?? ""]),
                ),
              });
              await qc.invalidateQueries({ queryKey: ["settings", brandId] });
            });
          }}
        >
          {(
            [
              "thankyou_html",
              "goodbye_html",
              "confirmation_email",
              "thankyou_email",
              "goodbye_email",
            ] as const
          ).map((k) => {
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
                <label className="flex items-center gap-2 text-sm font-medium">
                  <input
                    type="checkbox"
                    className="accent-primary"
                    checked={active}
                    disabled={q.data?.[k] !== undefined}
                    onChange={(e) => {
                      setEnabled(
                        e.target.checked
                          ? [...enabled, k]
                          : enabled.filter((x) => x !== k),
                      );
                      if (e.target.checked && k.endsWith("_email") && !form[k])
                        setForm({
                          ...form,
                          [k]: { subject: "", html: "", text: "" },
                        });
                    }}
                  />
                  {labels[k]}
                </label>
                {active &&
                  (k.endsWith("_html") ? (
                    <label className="block space-y-2 text-sm">
                      HTML
                      <Textarea
                        rows={5}
                        value={typeof value === "string" ? value : ""}
                        onChange={(e) =>
                          setForm({ ...form, [k]: e.target.value })
                        }
                      />
                    </label>
                  ) : (
                    <>
                      <Field
                        label="Subject"
                        required
                        value={(value as Email)?.subject ?? ""}
                        onChange={(e) =>
                          setForm({
                            ...form,
                            [k]: {
                              ...(value as Email),
                              subject: e.target.value,
                            },
                          })
                        }
                      />
                      <label className="block space-y-2 text-sm">
                        HTML
                        <Textarea
                          required
                          rows={5}
                          value={(value as Email)?.html ?? ""}
                          onChange={(e) =>
                            setForm({
                              ...form,
                              [k]: {
                                ...(value as Email),
                                html: e.target.value,
                              },
                            })
                          }
                        />
                      </label>
                      <label className="block space-y-2 text-sm">
                        Plain text
                        <Textarea
                          rows={3}
                          value={(value as Email)?.text ?? ""}
                          onChange={(e) =>
                            setForm({
                              ...form,
                              [k]: {
                                ...(value as Email),
                                text: e.target.value,
                              },
                            })
                          }
                        />
                      </label>
                    </>
                  ))}
              </div>
            );
          })}
          <Button disabled={a.busy || !enabled.length}>Save messages</Button>
          <Message {...a} />
        </form>
      )}
    </Section>
  );
}
