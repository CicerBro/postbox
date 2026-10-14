import { ConfirmDialog } from "../ui/confirm-dialog";
import { useState } from "react";
import { Webhook, Plus } from "lucide-react";
import { Badge } from "../ui/badge";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { useApp } from "../../lib/app-context";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";
import { Section, Field, Message, useAction, Secret } from "./Common";
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
function ConfirmWebhookAction({
  brandId,
  hook,
  action,
  onClose,
  onSaved,
}: {
  brandId: number;
  hook: Hook;
  action: "rotate" | "delete";
  onClose: () => void;
  onSaved: (hook?: Hook) => void;
}) {
  const rotate = action === "rotate";
  const mutation = useMutation({
    mutationFn: () =>
      api<Hook | undefined>(`/webhooks/${hook.id}`, {
        brandId,
        method: rotate ? "PATCH" : "DELETE",
        ...(rotate ? { body: { rotate_secret: true } } : {}),
      }),
    onSuccess: onSaved,
  });
  return (
    <ConfirmDialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      title={rotate ? "Rotate signing secret?" : "Delete webhook?"}
      description={
        <>
          {rotate
            ? "Update your receiver with the new secret immediately after rotation."
            : "This receiver will stop receiving events."}
          <span className="mt-3 block break-all">{hook.url}</span>
        </>
      }
      pending={mutation.isPending}
      error={mutation.error}
      variant={rotate ? "default" : "destructive"}
      confirmLabel={rotate ? "Rotate secret" : "Delete webhook"}
      pendingLabel={rotate ? "Rotating…" : "Deleting…"}
      onConfirm={() => mutation.mutateAsync()}
    />
  );
}

export function Webhooks({ brandId }: { brandId: number }) {
  const { session, brand } = useApp();
  const canWrite =
    session.admin ||
    (brand?.id === brandId && brand.permissions?.includes("write"));
  const qc = useQueryClient();
  const [edit, setEdit] = useState<Hook | null>(null);
  const [url, setUrl] = useState("");
  const [selected, setSelected] = useState(["subscribe"]);
  const [active, setActive] = useState(true);
  const [secret, setSecret] = useState("");
  const [confirmation, setConfirmation] = useState<{
    hook: Hook;
    action: "rotate" | "delete";
  } | null>(null);
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
      {!canWrite && (
        <p className="text-sm text-muted-foreground">
          You have read-only access to this brand.
        </p>
      )}
      {canWrite && (
        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (!canWrite) return;
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
            disabled={!canWrite || a.busy}
            value={url}
            onChange={(e) => setUrl(e.target.value)}
          />
          <div className="space-y-2">
            <p className="text-sm font-medium">Events to send</p>
            <div className="grid grid-cols-[repeat(auto-fit,minmax(min(100%,200px),1fr))] gap-x-6 gap-y-3">
              {events.map((event) => (
                <label
                  key={event}
                  htmlFor={`webhook-event-${event}`}
                  className="flex min-w-0 items-center gap-3 text-sm"
                >
                  <Switch
                    id={`webhook-event-${event}`}
                    checked={selected.includes(event)}
                    disabled={!canWrite || a.busy}
                    onCheckedChange={(checked) =>
                      setSelected((current) =>
                        checked
                          ? [...current, event]
                          : current.filter((value) => value !== event),
                      )
                    }
                  />
                  <span className="whitespace-nowrap">
                    {event.charAt(0).toUpperCase() +
                      event.slice(1).replaceAll("_", " ")}
                  </span>
                </label>
              ))}
            </div>
          </div>
          <div className="border-t pt-5">
            <label
              htmlFor="webhook-active"
              className="flex items-center gap-3 text-sm"
            >
              <Switch
                id="webhook-active"
                checked={active}
                disabled={!canWrite || a.busy}
                onCheckedChange={setActive}
              />
              Active
            </label>
          </div>
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
      )}
      <Message {...a} />
      {canWrite && secret && (
        <Secret value={secret} onDismiss={() => setSecret("")} />
      )}
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
            {canWrite
              ? "Create a receiver above to send brand events to your tools."
              : "This brand has no outbound event receivers."}
          </p>
        </div>
      ) : (
        <ul className="striped-list space-y-3">
          {q.data.pages.flat().map((h) => (
            <li key={h.id} className="rounded-xl border p-4 space-y-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <p className="row-title break-all text-lg font-medium">
                  {h.url}
                </p>
                <Badge variant={h.is_active ? "success" : "secondary"}>
                  {h.is_active ? "Active" : "Paused"}
                </Badge>
              </div>
              <div className="flex flex-wrap gap-1">
                {h.events.map((event) => (
                  <Badge key={event}>{event.replaceAll("_", " ")}</Badge>
                ))}
              </div>
              {canWrite && (
                <div className="flex flex-wrap gap-2">
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
                    onClick={() => setConfirmation({ hook: h, action: "rotate" })}
                  >
                    Rotate secret
                  </Button>
                  <div className="flex gap-2">
                    <Button
                      size="sm"
                      variant="default"
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
                      variant="destructive"
                      disabled={a.busy}
                      onClick={() => setConfirmation({ hook: h, action: "delete" })}
                    >
                      Delete
                    </Button>
                  </div>
                </div>
              )}
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
      {canWrite && confirmation && (
        <ConfirmWebhookAction
          brandId={brandId}
          {...confirmation}
          onClose={() => setConfirmation(null)}
          onSaved={(hook) => {
            if (confirmation.action === "delete" && edit?.id === confirmation.hook.id) reset();
            if (hook?.secret) setSecret(hook.secret);
            setConfirmation(null);
            void refresh();
          }}
        />
      )}
    </Section>
  );
}
