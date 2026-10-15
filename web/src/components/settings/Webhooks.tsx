import { InputField } from "../ui/field";
import { ConfirmDialog } from "../ui/confirm-dialog";
import { useState } from "react";
import { MoreHorizontal, Plus, Webhook } from "lucide-react";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import {
  useInfiniteQuery,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query";
import { api } from "../../lib/api";
import { useApp } from "../../lib/app-context";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import { Section, Message, useAction, Secret } from "./Common";

type Hook = {
  id: number;
  url: string;
  events: string[];
  is_active: boolean;
  secret?: string;
};
const eventGroups = [
  { title: "Subscribers", events: ["subscribe", "unsubscribe"] },
  { title: "Delivery", events: ["bounce", "complaint"] },
  { title: "Campaigns", events: ["campaign_started", "campaign_finished"] },
];
function eventLabel(event: string) {
  return event.charAt(0).toUpperCase() + event.slice(1).replaceAll("_", " ");
}

function WebhookEditor({
  brandId,
  hook,
  onClose,
  onSaved,
}: {
  brandId: number;
  hook: Hook | null;
  onClose: () => void;
  onSaved: (hook: Hook) => void;
}) {
  const [url, setUrl] = useState(hook?.url ?? "");
  const [selected, setSelected] = useState(hook?.events ?? ["subscribe"]);
  const [active, setActive] = useState(hook?.is_active ?? true);
  const save = useMutation({
    mutationFn: () =>
      api<Hook>(hook ? `/webhooks/${hook.id}` : "/webhooks", {
        method: hook ? "PATCH" : "POST",
        brandId,
        body: { url, events: selected, is_active: active },
      }),
    onSuccess: onSaved,
  });
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !save.isPending) onClose();
      }}
    >
      <DialogContent
        width="lg"
        showCloseButton={!save.isPending}
        aria-busy={save.isPending}
        onOverlayClick={() => {
          if (!save.isPending) onClose();
        }}
      >
        <DialogHeader>
          <DialogTitle>{hook ? "Edit webhook" : "Add webhook"}</DialogTitle>
          <DialogDescription>
            Choose where events are sent and which events to include.
          </DialogDescription>
        </DialogHeader>
        <form
          className="space-y-5"
          onSubmit={(event) => {
            event.preventDefault();
            if (selected.length && !save.isPending) save.mutate();
          }}
        >
          <div className="space-y-2">
            <InputField
              label="Destination URL"
              placeholder="https://your-app.com/webhooks/postbox"
              type="url"
              required
              value={url}
              disabled={save.isPending}
              onChange={(event) => setUrl(event.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              Use a public HTTPS endpoint. Events are HMAC-signed and retried on
              failure.
            </p>
          </div>
          <fieldset disabled={save.isPending}>
            <legend className="mb-3 text-sm font-medium">Events to send</legend>
            <div className="grid gap-5 sm:grid-cols-3">
              {eventGroups.map((group) => (
                <div key={group.title} className="space-y-3">
                  <p className="text-xs text-muted-foreground">{group.title}</p>
                  {group.events.map((event) => (
                    <label
                      key={event}
                      className="flex items-center gap-2 text-sm"
                    >
                      <Checkbox
                        checked={selected.includes(event)}
                        disabled={save.isPending}
                        onCheckedChange={(checked) =>
                          setSelected((current) =>
                            checked === true
                              ? [...current, event]
                              : current.filter((value) => value !== event),
                          )
                        }
                      />
                      {eventLabel(event)}
                    </label>
                  ))}
                </div>
              ))}
            </div>
          </fieldset>
          <label className="flex items-center gap-2 border-t pt-4 text-sm">
            <Checkbox
              checked={active}
              disabled={save.isPending}
              onCheckedChange={(checked) => setActive(checked === true)}
            />
            Active
          </label>
          <Message error={save.error} />
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={save.isPending}
              onClick={onClose}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={save.isPending || !selected.length}>
              {save.isPending
                ? "Saving…"
                : hook
                  ? "Save changes"
                  : "Create webhook"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

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
  const [editor, setEditor] = useState<{ hook: Hook | null } | null>(null);
  const [confirmation, setConfirmation] = useState<{
    hook: Hook;
    action: "rotate" | "delete";
  } | null>(null);
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
  function refresh() {
    return qc.invalidateQueries({ queryKey: ["webhooks", brandId] });
  }
  function saved(hook?: Hook) {
    if (hook?.secret) setSecret(hook.secret);
    setEditor(null);
    setConfirmation(null);
    void refresh();
  }
  return (
    <Section
      title="Webhooks"
      description="Send subscriber and campaign events to your tools."
      action={
        canWrite && (
          <Button onClick={() => setEditor({ hook: null })} disabled={a.busy}>
            <Plus size={16} aria-hidden="true" />
            Add webhook
          </Button>
        )
      }
    >
      {!canWrite && (
        <p className="text-sm text-muted-foreground">
          You have read-only access to this brand.
        </p>
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
              ? "Add a webhook to send brand events to your tools."
              : "This brand has no outbound event receivers."}
          </p>
        </div>
      ) : (
        <div className="table-card relative overflow-x-auto">
          <table className="w-full min-w-[640px] table-fixed text-left text-sm">
            <caption className="sr-only">Brand webhooks</caption>
            <thead>
              <tr>
                <th className="w-[37%]">Destination</th>
                <th className="w-[30%]">Events</th>
                <th className="w-[13%]">Status</th>
                {canWrite && (
                  <th className="w-[20%]">
                    <span className="sr-only">Actions</span>
                  </th>
                )}
              </tr>
            </thead>
            <tbody>
              {q.data.pages.flat().map((h) => (
                <tr className="border-t" key={h.id}>
                  <td className="p-3">
                    <span className="break-all">{h.url}</span>
                  </td>
                  <td className="p-3">
                    <span className="leading-relaxed">
                      {h.events.map(eventLabel).join(", ")}
                    </span>
                  </td>
                  <td className="p-3">
                    <Badge variant={h.is_active ? "success" : "secondary"}>
                      <span className="status-dot" />
                      {h.is_active ? "Active" : "Paused"}
                    </Badge>
                  </td>
                  {canWrite && (
                    <td className="p-3">
                      <div className="flex items-center justify-end gap-2">
                        <Button
                          size="sm"
                          variant="outline"
                          disabled={a.busy}
                          onClick={() => setEditor({ hook: h })}
                        >
                          Edit
                        </Button>
                        <DropdownMenu.Root>
                          <DropdownMenu.Trigger asChild>
                            <Button
                              size="icon"
                              variant="outline"
                              disabled={a.busy}
                              aria-label={`More actions for ${h.url}`}
                            >
                              <MoreHorizontal size={16} aria-hidden="true" />
                            </Button>
                          </DropdownMenu.Trigger>
                          <DropdownMenu.Portal>
                            <DropdownMenu.Content
                              className="dropdown-content"
                              align="end"
                              sideOffset={6}
                            >
                              <DropdownMenu.Item
                                onSelect={() =>
                                  void a.run(async () => {
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
                              </DropdownMenu.Item>
                              <DropdownMenu.Item
                                onSelect={() =>
                                  setConfirmation({ hook: h, action: "rotate" })
                                }
                              >
                                Rotate secret
                              </DropdownMenu.Item>
                              <DropdownMenu.Separator className="dropdown-separator" />
                              <DropdownMenu.Item
                                className="text-destructive"
                                onSelect={() =>
                                  setConfirmation({ hook: h, action: "delete" })
                                }
                              >
                                Delete webhook
                              </DropdownMenu.Item>
                            </DropdownMenu.Content>
                          </DropdownMenu.Portal>
                        </DropdownMenu.Root>
                      </div>
                    </td>
                  )}
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
      {canWrite && editor && (
        <WebhookEditor
          brandId={brandId}
          hook={editor.hook}
          onClose={() => setEditor(null)}
          onSaved={saved}
        />
      )}
      {canWrite && confirmation && (
        <ConfirmWebhookAction
          brandId={brandId}
          {...confirmation}
          onClose={() => setConfirmation(null)}
          onSaved={saved}
        />
      )}
    </Section>
  );
}
