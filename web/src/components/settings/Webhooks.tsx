import { useState } from "react";
import { Webhook, Plus } from "lucide-react";
import { Badge } from "../ui/badge";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { Button } from "../ui/button";
import { Section, Field, Message, useAction, Secret, Checks } from "./Common";
type Hook = {
  id: number;
  url: string;
  events: string[];
  is_active: boolean;
  secret?: string;
};
const events = [
  "subscribe",
  "unsubscribe",
  "bounce",
  "complaint",
  "campaign_started",
  "campaign_finished",
];
export function Webhooks({ brandId }: { brandId: number }) {
  const qc = useQueryClient();
  const [edit, setEdit] = useState<Hook | null>(null);
  const [url, setUrl] = useState("");
  const [selected, setSelected] = useState(["subscribe"]);
  const [active, setActive] = useState(true);
  const [secret, setSecret] = useState("");
  const a = useAction();
  const q = useInfiniteQuery({
    queryKey: ["webhooks", brandId],
    initialPageParam: 0,
    queryFn: ({ pageParam }) =>
      api<Hook[]>(`/webhooks?after=${pageParam}&limit=100`, { brandId }),
    getNextPageParam: (rows) =>
      rows.length === 100 ? rows.at(-1)?.id : undefined,
  });
  async function refresh() {
    await qc.invalidateQueries({ queryKey: ["webhooks", brandId] });
  }
  function reset() {
    setEdit(null);
    setUrl("");
    setSelected(["subscribe"]);
    setActive(true);
  }
  return (
    <Section title="Webhooks">
      <p className="text-sm text-muted-foreground">
        Outbound events are HMAC-signed and retried by the job runner. Use a
        public HTTPS receiver; unsafe destinations are rejected by the server.
      </p>
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          void a.run(
            async () => {
              const r = await api<Hook>(
                edit ? `/webhooks/${edit.id}` : "/webhooks",
                {
                  method: edit ? "PATCH" : "POST",
                  brandId,
                  body: { url, events: selected, is_active: active },
                },
              );
              if (r.secret) setSecret(r.secret);
              reset();
              await refresh();
            },
            edit ? "Webhook updated." : "Webhook created.",
          );
        }}
      >
        <Field
          label="Destination URL"
          placeholder="https://your-app.com/webhooks/postbox"
          type="url"
          required
          value={url}
          onChange={(e) => setUrl(e.target.value)}
        />
        <div className="space-y-2">
          <p className="text-sm font-medium">Events to send</p>
          <Checks values={events} selected={selected} onChange={setSelected} />
        </div>
        <label className="flex gap-2 text-sm">
          <input
            type="checkbox"
            checked={active}
            onChange={(e) => setActive(e.target.checked)}
          />
          Active
        </label>
        <div className="flex gap-2">
          <Button disabled={a.busy || !selected.length}>
            <Plus size={16} aria-hidden="true" />
            {edit ? "Save webhook" : "Create webhook"}
          </Button>
          {edit && (
            <Button type="button" variant="ghost" onClick={reset}>
              Cancel
            </Button>
          )}
        </div>
      </form>
      <Message {...a} />
      {secret && <Secret value={secret} onDismiss={() => setSecret("")} />}
      <Message error={q.error} />
      {q.isPending ? (
        <p role="status">Loading webhooks…</p>
      ) : q.isError ? (
        <Button variant="outline" onClick={() => q.refetch()}>
          Retry
        </Button>
      ) : !q.data?.pages.flat().length ? (
        <div className="empty-panel">
          <Webhook size={26} aria-hidden="true" />
          <h3>No webhooks configured</h3>
          <p className="page-subtitle">
            Create a receiver above to send brand events to your tools.
          </p>
        </div>
      ) : (
        <ul className="space-y-3">
          {q.data.pages.flat().map((h) => (
            <li key={h.id} className="rounded-xl border p-4 space-y-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <p className="row-title break-all">{h.url}</p>
                <Badge variant={h.is_active ? "success" : "secondary"}>
                  {h.is_active ? "Active" : "Paused"}
                </Badge>
              </div>
              <p className="text-xs text-muted-foreground">
                {h.events
                  .map((event) => event.replaceAll("_", " "))
                  .join(" · ")}
              </p>
              <div className="flex flex-wrap gap-2">
                <Button
                  size="sm"
                  variant="outline"
                  disabled={a.busy}
                  onClick={() => {
                    setEdit(h);
                    setUrl(h.url);
                    setSelected(h.events);
                    setActive(h.is_active);
                  }}
                >
                  Edit
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={a.busy}
                  onClick={() =>
                    a.run(async () => {
                      await api(`/webhooks/${h.id}`, {
                        method: "PATCH",
                        brandId,
                        body: { is_active: !h.is_active },
                      });
                      await refresh();
                    }, "Webhook status updated.")
                  }
                >
                  {h.is_active ? "Pause" : "Activate"}
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={a.busy}
                  onClick={() => {
                    if (
                      window.confirm(
                        "Rotate this signing secret? Update your receiver with the new secret immediately.",
                      )
                    )
                      void a.run(async () => {
                        const r = await api<Hook>(`/webhooks/${h.id}`, {
                          method: "PATCH",
                          brandId,
                          body: { rotate_secret: true },
                        });
                        setSecret(r.secret ?? "");
                        await refresh();
                      }, "Signing secret rotated.");
                  }}
                >
                  Rotate secret
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={a.busy}
                  onClick={() => {
                    if (window.confirm("Delete this webhook?"))
                      void a.run(async () => {
                        await api(`/webhooks/${h.id}`, {
                          method: "DELETE",
                          brandId,
                        });
                        if (edit?.id === h.id) reset();
                        await refresh();
                      }, "Webhook deleted.");
                  }}
                >
                  Delete
                </Button>
              </div>
            </li>
          ))}
        </ul>
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
