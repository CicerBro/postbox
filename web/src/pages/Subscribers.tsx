import { SelectField } from "../components/ui/select";
import { Checkbox } from "../components/ui/checkbox";
import { useEffect, useState } from "react";
import {
  Plus,
  Check,
  Search,
  SlidersHorizontal,
  Users,
  List as ListIcon,
  MoreHorizontal,
} from "lucide-react";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Textarea } from "../components/ui/textarea";
import { Badge } from "../components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "../components/ui/dialog";
import {
  allPages,
  SubscriberStatusBadge,
  subscriberInitials,
  type SubscriberList,
  type CustomField,
  type Subscriber,
  ErrorMessage,
  Field,
  fieldValue,
  parseFields,
  tableClass,
  useSubscriberAction,
  useSubscribersQuery,
} from "../components/subscribers/shared";
import {
  CSVImport,
  ExportSubscribers,
  Housekeeping,
  ListForm,
  MassOperations,
} from "../components/subscribers/Operations";

type Membership = {
  list_id: number;
  status: string;
  created_at: number;
  updated_at: number;
};
type Activity = { id: number; kind: string; at: number; detail: string };
const statuses = ["active", "bounced", "complained", "blocklisted"];

function SubscriberProfile({
  brandId,
  subscriber,
  fields,
  lists,
  onClose,
}: {
  brandId: number;
  subscriber: Subscriber | null;
  fields: CustomField[];
  lists: SubscriberList[];
  onClose: () => void;
}) {
  const [email, setEmail] = useState(subscriber?.email || "");
  const [name, setName] = useState(subscriber?.name || "");
  const [status, setStatus] = useState(subscriber?.status || "active");
  const [json, setJson] = useState(
    JSON.stringify(subscriber ? parseFields(subscriber.fields) : {}, null, 2),
  );
  const [creationLists, setCreationLists] = useState<number[]>([]);
  const [consent, setConsent] = useState(false);
  const [consentIP, setConsentIP] = useState("");
  const [error, setError] = useState<unknown>();
  const [selectedList, setSelectedList] = useState("");
  const [membershipConsent, setMembershipConsent] = useState(false);
  const [erase, setErase] = useState(false);
  const [eraseEmail, setEraseEmail] = useState("");
  const [unsubscribeList, setUnsubscribeList] = useState<number | null>(null);
  const [activityCursor, setActivityCursor] = useState(0);
  const action = useSubscriberAction<Subscriber>(brandId);
  const membershipAction = useSubscriberAction(brandId);
  const memberships = useSubscribersQuery<Membership[]>(
    brandId,
    `memberships-${subscriber?.id || 0}`,
    `/subscribers/${subscriber?.id || 0}/memberships`,
    !!subscriber,
  );
  // New profiles have no activity or memberships, so avoid querying ID zero.
  const timeline = useQuery({
    queryKey: [
      "subscribers",
      brandId,
      "activity",
      subscriber?.id,
      activityCursor,
    ],
    queryFn: ({ signal }) =>
      api<Activity[]>(
        `/subscribers/${subscriber!.id}/activity?after=${activityCursor}&limit=100`,
        { brandId, signal },
      ),
    enabled: !!subscriber && brandId > 0,
  });
  const existing = subscriber ? memberships.data || [] : [];
  const save = async () => {
    setError(undefined);
    try {
      const data = JSON.parse(json);
      if (!data || typeof data !== "object" || Array.isArray(data))
        throw new Error("Custom fields must be a JSON object.");
      for (const f of fields) {
        const v = data[f.key];
        if (v === null || v === undefined) continue;
        if (
          f.type === "number" &&
          (typeof v !== "number" || !Number.isFinite(v))
        )
          throw new Error(`${f.label} must be a finite number.`);
        if (f.type === "boolean" && typeof v !== "boolean")
          throw new Error(`${f.label} must be true or false.`);
        if (
          ["text", "date", "select"].includes(f.type) &&
          typeof v !== "string"
        )
          throw new Error(`${f.label} must be text.`);
      }
      await action.mutateAsync({
        path: subscriber ? `/subscribers/${subscriber.id}` : "/subscribers",
        method: subscriber ? "PATCH" : "POST",
        body: {
          email: email.trim(),
          name,
          fields: data,
          status,
          list_ids: subscriber ? undefined : creationLists,
          consent,
          consent_ip: consent ? consentIP : "",
          resubscribe: false,
        },
      });
      onClose();
    } catch (e) {
      setError(e);
    }
  };
  const typedFields = (() => {
    try {
      const v = JSON.parse(json);
      return v && typeof v === "object" && !Array.isArray(v) ? v : {};
    } catch {
      return {};
    }
  })();
  const setTyped = (field: CustomField, value: string) => {
    try {
      const data = JSON.parse(json);
      if (!data || Array.isArray(data) || typeof data !== "object")
        throw new Error("Repair the JSON object before editing fields.");
      setJson(
        JSON.stringify(
          { ...data, [field.key]: fieldValue(field.type, value) },
          null,
          2,
        ),
      );
      setError(undefined);
    } catch (e) {
      setError(e);
    }
  };
  return (
    <>
      <Dialog open onOpenChange={(open) => !open && onClose()}>
        <DialogContent className="max-w-3xl max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>
              {subscriber ? "Subscriber profile" : "New subscriber"}
            </DialogTitle>
          </DialogHeader>
          {subscriber && (
            <div className="flex items-center gap-3 rounded-lg border bg-muted/30 p-4">
              <span className="avatar" aria-hidden="true">
                {subscriberInitials(subscriber.name, subscriber.email)}
              </span>
              <div className="flex-1">
                <p className="row-title">
                  {subscriber.name || "Unnamed subscriber"}
                </p>
                <p className="row-subtitle">{subscriber.email}</p>
              </div>
              <SubscriberStatusBadge status={subscriber.status} />
            </div>
          )}
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Email">
              <Input
                type="email"
                value={email}
                disabled={!!subscriber}
                onChange={(e) => setEmail(e.target.value)}
              />
            </Field>
            <Field label="Name">
              <Input value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field label="Subscriber status">
              <SelectField
                value={status}
                disabled={!!subscriber && subscriber.status !== "active"}
                onValueChange={(value) => setStatus(value)}
                options={statuses.map((s) => ({ value: s, label: s }))}
              />
            </Field>
            {subscriber && (
              <div className="text-sm text-muted-foreground self-center">
                Created {new Date(subscriber.created_at).toLocaleString()}
                <br />
                Updated {new Date(subscriber.updated_at).toLocaleString()}
              </div>
            )}
          </div>
          {!!fields.length && (
            <div className="grid gap-3 sm:grid-cols-2">
              {fields.map((f) => (
                <Field key={f.key} label={`${f.label} (${f.type})`}>
                  {f.type === "boolean" ? (
                    <SelectField
                      value={String(
                        typedFields[f.key] === null ||
                          typedFields[f.key] === undefined
                          ? ""
                          : String(typedFields[f.key]),
                      )}
                      onValueChange={(value) => setTyped(f, value)}
                      options={[
                        { value: "", label: "Not set" },
                        { value: "true", label: "True" },
                        { value: "false", label: "False" },
                      ]}
                    />
                  ) : (
                    <Input
                      type={
                        f.type === "number"
                          ? "number"
                          : f.type === "date"
                            ? "date"
                            : "text"
                      }
                      step={f.type === "number" ? "any" : undefined}
                      value={
                        typedFields[f.key] === null ||
                        typedFields[f.key] === undefined
                          ? ""
                          : String(typedFields[f.key])
                      }
                      onChange={(e) => setTyped(f, e.target.value)}
                    />
                  )}
                </Field>
              ))}
            </div>
          )}
          <Field label="Custom fields JSON">
            <Textarea
              className="font-mono text-xs"
              rows={5}
              value={json}
              onChange={(e) => setJson(e.target.value)}
            />
          </Field>
          <div className="rounded-lg border p-4 space-y-3">
            <p className="text-sm">
              Existing consent:{" "}
              {subscriber?.gdpr_consent_at
                ? new Date(subscriber.gdpr_consent_at).toLocaleString()
                : "Not recorded"}
              {subscriber?.gdpr_consent_ip
                ? `, IP ${subscriber.gdpr_consent_ip}`
                : ""}
            </p>
            <label className="flex items-center gap-2 text-sm">
              <Checkbox
                checked={consent}
                onCheckedChange={(checked) => setConsent(checked === true)}
              />
              Record newly obtained GDPR consent with the current timestamp
            </label>
            {consent && (
              <Field label="Consent IP (optional)">
                <Input
                  value={consentIP}
                  onChange={(e) => setConsentIP(e.target.value)}
                />
              </Field>
            )}
          </div>
          {!subscriber && (
            <fieldset className="space-y-2">
              <legend className="text-sm font-medium">
                Initial list memberships
              </legend>
              <div className="flex gap-4 flex-wrap">
                {lists.map((l) => (
                  <label className="flex gap-2 items-center text-sm" key={l.id}>
                    <Checkbox
                      checked={creationLists.includes(l.id)}
                      onCheckedChange={(checked) =>
                        setCreationLists(
                          checked === true
                            ? [...creationLists, l.id]
                            : creationLists.filter((id) => id !== l.id),
                        )
                      }
                    />
                    {l.name} ({l.opt_in})
                  </label>
                ))}
              </div>
              <p className="text-xs text-muted-foreground">
                Record explicit consent above before subscribing. Double opt-in
                memberships require confirmation.
              </p>
            </fieldset>
          )}
          <ErrorMessage error={error || action.error} />
          <div className="flex justify-end">
            <Button
              disabled={
                action.isPending ||
                !email.trim() ||
                (!subscriber && creationLists.length > 0 && !consent)
              }
              onClick={save}
            >
              {action.isPending ? "Saving…" : "Save profile"}
            </Button>
          </div>
          {subscriber && (
            <section className="border-t pt-4 space-y-4">
              <h3 className="section-heading font-semibold">
                List memberships
              </h3>
              <ErrorMessage
                error={memberships.error || membershipAction.error}
              />
              {memberships.isPending ? (
                <p role="status">Loading memberships…</p>
              ) : !existing.length ? (
                <p className="text-sm text-muted-foreground">
                  No list memberships.
                </p>
              ) : (
                <table className={tableClass}>
                  <thead>
                    <tr>
                      <th>List</th>
                      <th>Status</th>
                      <th>Action</th>
                    </tr>
                  </thead>
                  <tbody>
                    {existing.map((m) => (
                      <tr key={m.list_id}>
                        <td>
                          {lists.find((l) => l.id === m.list_id)?.name ||
                            `List ${m.list_id}`}
                        </td>
                        <td>
                          <SubscriberStatusBadge status={m.status} />
                        </td>
                        <td>
                          {m.status !== "unsubscribed" && (
                            <Button
                              variant="outline"
                              size="sm"
                              onClick={() => {
                                membershipAction.reset();
                                setUnsubscribeList(m.list_id);
                              }}
                            >
                              Unsubscribe
                            </Button>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
              <Field label="Add or resubscribe to a list">
                <SelectField
                  value={String(selectedList)}
                  onValueChange={(value) => {
                    setSelectedList(value);
                    setMembershipConsent(false);
                  }}
                  options={[
                    { value: "", label: "Choose list" },
                    ...lists.map((l) => ({
                      value: String(l.id),
                      label: `${l.name} (${l.opt_in} opt-in)`,
                    })),
                  ]}
                />
              </Field>
              <label className="flex gap-2 text-sm items-start">
                <Checkbox
                  checked={membershipConsent}
                  onCheckedChange={(checked) =>
                    setMembershipConsent(checked === true)
                  }
                />
                I have explicit permission to subscribe this subscriber,
                including any previous opt-out.
              </label>
              <Button
                variant="outline"
                disabled={
                  !selectedList ||
                  !membershipConsent ||
                  membershipAction.isPending ||
                  subscriber.status !== "active"
                }
                onClick={async () => {
                  try {
                    const current = await api<Subscriber>(
                      `/subscribers/by-email?email=${encodeURIComponent(subscriber.email)}`,
                      { brandId },
                    );
                    await membershipAction.mutateAsync({
                      path: `/subscribers/${subscriber.id}`,
                      method: "PATCH",
                      body: {
                        email: current.email,
                        name: current.name,
                        fields: parseFields(current.fields),
                        status: current.status,
                        list_ids: [Number(selectedList)],
                        resubscribe: true,
                        consent: true,
                      },
                    });
                    setSelectedList("");
                    setMembershipConsent(false);
                  } catch (e) {
                    setError(e);
                  }
                }}
              >
                Subscribe to selected list
              </Button>
              <p className="text-xs text-muted-foreground">
                Double opt-in lists require confirmation. Bounced, complained or
                blocklisted profiles cannot be reactivated here.
              </p>
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  membershipAction.reset();
                  setUnsubscribeList(0);
                }}
              >
                Unsubscribe from all lists
              </Button>
            </section>
          )}
          {subscriber && (
            <section className="border-t pt-4 space-y-3">
              <h3 className="section-heading font-semibold">
                Activity timeline
              </h3>
              <ErrorMessage error={timeline.error} />
              {timeline.isPending ? (
                <p role="status">Loading activity…</p>
              ) : !timeline.data?.length ? (
                <p className="text-sm text-muted-foreground">
                  No activity on this page.
                </p>
              ) : (
                <ol className="space-y-3">
                  {timeline.data.map((a) => (
                    <li key={a.id} className="border-l-2 pl-3">
                      <div className="text-sm font-medium">
                        {a.kind}{" "}
                        <span className="text-muted-foreground font-normal">
                          {new Date(a.at).toLocaleString()}
                        </span>
                      </div>
                      <p className="text-xs text-muted-foreground break-all">
                        {a.detail}
                      </p>
                    </li>
                  ))}
                </ol>
              )}
              <div className="flex gap-2">
                {activityCursor > 0 && (
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => setActivityCursor(0)}
                  >
                    First activity
                  </Button>
                )}
                {timeline.data?.length === 100 && (
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => setActivityCursor(timeline.data![99].id)}
                  >
                    Next activity
                  </Button>
                )}
              </div>
            </section>
          )}
          {subscriber && (
            <div className="border-t pt-4">
              <Button
                variant="destructive"
                size="sm"
                onClick={() => {
                  action.reset();
                  setErase(true);
                }}
              >
                GDPR erase subscriber
              </Button>
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={onClose}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog open={erase} onOpenChange={setErase}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Permanently erase subscriber?</DialogTitle>
          </DialogHeader>
          <p className="text-sm">
            Deletes {subscriber?.email}, list memberships, raw opens, clicks and
            associated event history. This cannot be undone.
          </p>
          <Field label="Type the email to confirm">
            <Input
              value={eraseEmail}
              onChange={(e) => setEraseEmail(e.target.value)}
            />
          </Field>
          <ErrorMessage error={action.error} />
          <DialogFooter>
            <Button
              variant="outline"
              disabled={action.isPending}
              onClick={() => setErase(false)}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={eraseEmail !== subscriber?.email || action.isPending}
              onClick={async () => {
                try {
                  await action.mutateAsync({
                    path: `/subscribers/${subscriber?.id}`,
                    method: "DELETE",
                  });
                  onClose();
                } catch {
                  /* displayed */
                }
              }}
            >
              Permanently erase
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog
        open={unsubscribeList !== null}
        onOpenChange={(open) => !open && setUnsubscribeList(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Unsubscribe subscriber?</DialogTitle>
          </DialogHeader>
          <p className="text-sm">
            Unsubscribe {subscriber?.email} from{" "}
            {unsubscribeList === 0
              ? "all lists in this brand"
              : lists.find((l) => l.id === unsubscribeList)?.name}
            . Pending campaign recipients will be skipped when they are no
            longer in the audience.
          </p>
          <ErrorMessage error={membershipAction.error} />
          <DialogFooter>
            <Button variant="outline" onClick={() => setUnsubscribeList(null)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={membershipAction.isPending}
              onClick={async () => {
                try {
                  await membershipAction.mutateAsync({
                    path: `/subscribers/${subscriber?.id}/unsubscribe`,
                    body: { list_id: unsubscribeList },
                  });
                  setUnsubscribeList(null);
                } catch {
                  /* displayed */
                }
              }}
            >
              Unsubscribe
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function CustomFields({
  brandId,
  fields,
}: {
  brandId: number;
  fields: CustomField[];
}) {
  const [editing, setEditing] = useState<CustomField | null>(null);
  const action = useSubscriberAction(brandId);
  const blank = {
    id: 0,
    key: "",
    label: "",
    type: "text",
    is_filterable: false,
  };
  return (
    <section className="space-y-4">
      <div className="flex justify-between">
        <div>
          <h2 className="text-lg font-semibold">Custom fields</h2>
          <p className="text-sm text-muted-foreground">
            Filterable fields create database indexes for segment rules.
          </p>
        </div>
        <Button
          variant="outline"
          onClick={() => {
            action.reset();
            setEditing(blank);
          }}
        >
          New field
        </Button>
      </div>
      {!fields.length ? (
        <p className="text-sm text-muted-foreground">
          No custom fields defined.
        </p>
      ) : (
        <div className="table-card rounded-lg border overflow-x-auto">
          <table className={tableClass}>
            <thead>
              <tr>
                <th>Label</th>
                <th>Key</th>
                <th>Type</th>
                <th>Filterable</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {fields.map((f) => (
                <tr key={f.id}>
                  <td>{f.label}</td>
                  <td>
                    <code>{f.key}</code>
                  </td>
                  <td>
                    <Badge variant="outline">{f.type}</Badge>
                  </td>
                  <td>{f.is_filterable ? "Yes" : "No"}</td>
                  <td>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => {
                        action.reset();
                        setEditing({ ...f });
                      }}
                    >
                      Edit
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <Dialog
        open={!!editing}
        onOpenChange={(open) => !open && setEditing(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {editing?.id ? "Edit custom field" : "New custom field"}
            </DialogTitle>
          </DialogHeader>
          {editing && (
            <>
              <Field label="Key">
                <Input
                  disabled={editing.id > 0}
                  value={editing.key}
                  onChange={(e) =>
                    setEditing({ ...editing, key: e.target.value })
                  }
                />
              </Field>
              <Field label="Label">
                <Input
                  value={editing.label}
                  onChange={(e) =>
                    setEditing({ ...editing, label: e.target.value })
                  }
                />
              </Field>
              <Field label="Type">
                <SelectField
                  value={editing.type}
                  onValueChange={(value) =>
                    setEditing({ ...editing, type: value })
                  }
                  options={["text", "number", "date", "boolean", "select"].map(
                    (t) => ({ value: t, label: t }),
                  )}
                />
              </Field>
              <p className="text-xs text-muted-foreground">
                Changing type does not convert existing values. Select fields
                store text values.
              </p>
              <label className="text-sm flex items-center gap-2">
                <Checkbox
                  checked={editing.is_filterable}
                  onCheckedChange={(checked) =>
                    setEditing({ ...editing, is_filterable: checked === true })
                  }
                />
                Filterable in segments
              </label>
            </>
          )}
          <ErrorMessage error={action.error} />
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditing(null)}>
              Cancel
            </Button>
            <Button
              disabled={!editing?.key || !editing.label || action.isPending}
              onClick={async () => {
                if (!editing) return;
                try {
                  await action.mutateAsync({
                    path: editing.id
                      ? `/custom-fields/${editing.id}`
                      : "/custom-fields",
                    method: editing.id ? "PATCH" : "POST",
                    body: {
                      key: editing.key,
                      label: editing.label,
                      type: editing.type,
                      is_filterable: editing.is_filterable,
                    },
                  });
                  setEditing(null);
                } catch {
                  /* displayed */
                }
              }}
            >
              Save field
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  );
}

type NotificationEmail = { subject: string; html: string; text?: string };
type ListNotificationKey =
  | "confirmation_email"
  | "confirmation_page_html"
  | "thankyou_email"
  | "thankyou_html"
  | "goodbye_email"
  | "goodbye_html";
type ListNotifications = Partial<
  Record<ListNotificationKey, string | NotificationEmail>
>;
const notificationNames: {
  key: ListNotificationKey;
  label: string;
  email?: boolean;
}[] = [
  { key: "confirmation_email", label: "Confirmation email", email: true },
  { key: "confirmation_page_html", label: "Pending confirmation page" },
  { key: "thankyou_email", label: "Welcome email", email: true },
  { key: "thankyou_html", label: "Thank-you page" },
  { key: "goodbye_email", label: "Goodbye email", email: true },
  { key: "goodbye_html", label: "Goodbye page" },
];
function ListNotificationsDialog({
  brandId,
  list,
  onClose,
}: {
  brandId: number;
  list: SubscriberList;
  onClose: () => void;
}) {
  const settings = useSubscribersQuery<{
    overrides: ListNotifications;
    effective: ListNotifications;
  }>(brandId, `list-settings-${list.id}`, `/lists/${list.id}/settings`);
  const [draft, setDraft] = useState<ListNotifications>({});
  const [ready, setReady] = useState(false);
  const action = useSubscriberAction(brandId);
  useEffect(() => {
    if (settings.data && !ready) {
      setDraft(settings.data.overrides);
      setReady(true);
    }
  }, [settings.data, ready]);
  const emailValue = (key: ListNotificationKey): NotificationEmail => {
    const value = draft[key];
    return typeof value === "object"
      ? value
      : { subject: "", html: "", text: "" };
  };
  const invalid = notificationNames.some(
    (n) =>
      n.email &&
      draft[n.key] !== undefined &&
      (!emailValue(n.key).subject.trim() || !emailValue(n.key).html.trim()),
  );
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-3xl max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{list.name} messages and pages</DialogTitle>
        </DialogHeader>
        <p className="text-sm text-muted-foreground">
          Use brand defaults or customize messages for this list. Welcome emails
          are sent after subscribed consent. Confirmation emails support{" "}
          {"{{ confirmation_url }}"}.
        </p>
        <ErrorMessage error={settings.error || action.error} />
        {settings.isPending ? (
          <p role="status">Loading settings…</p>
        ) : (
          ready &&
          notificationNames.map((n) => (
            <fieldset key={n.key} className="border rounded-lg p-4 space-y-3">
              <legend className="font-medium text-sm px-1">{n.label}</legend>
              <label className="flex gap-2 text-sm items-center">
                <Checkbox
                  checked={draft[n.key] !== undefined}
                  onCheckedChange={(checked) =>
                    setDraft((current) => {
                      const next = { ...current };
                      if (checked === true)
                        next[n.key] =
                          settings.data?.effective[n.key] ||
                          (n.email ? { subject: "", html: "", text: "" } : "");
                      else delete next[n.key];
                      return next;
                    })
                  }
                />
                Customize for this list
              </label>
              {draft[n.key] === undefined ? (
                <p className="text-xs text-muted-foreground">
                  {settings.data?.effective[n.key]
                    ? n.key === "confirmation_page_html" &&
                      settings.data?.overrides.thankyou_html
                      ? "Uses this list's thank-you page."
                      : "Inherited from brand settings."
                    : n.email && n.key !== "confirmation_email"
                      ? "No email configured in brand settings."
                      : "Uses the default hosted page or confirmation message."}
                </p>
              ) : n.email ? (
                <>
                  <Field label="Subject">
                    <Input
                      value={emailValue(n.key).subject}
                      onChange={(e) =>
                        setDraft({
                          ...draft,
                          [n.key]: {
                            ...emailValue(n.key),
                            subject: e.target.value,
                          },
                        })
                      }
                    />
                  </Field>
                  <Field label="Email HTML">
                    <Textarea
                      rows={5}
                      className="font-mono text-xs"
                      value={emailValue(n.key).html}
                      onChange={(e) =>
                        setDraft({
                          ...draft,
                          [n.key]: {
                            ...emailValue(n.key),
                            html: e.target.value,
                          },
                        })
                      }
                    />
                  </Field>
                  <Field label="Plain text (optional)">
                    <Textarea
                      rows={3}
                      value={emailValue(n.key).text || ""}
                      onChange={(e) =>
                        setDraft({
                          ...draft,
                          [n.key]: {
                            ...emailValue(n.key),
                            text: e.target.value,
                          },
                        })
                      }
                    />
                  </Field>
                </>
              ) : (
                <Field label="Page HTML">
                  <Textarea
                    rows={6}
                    className="font-mono text-xs"
                    value={
                      typeof draft[n.key] === "string"
                        ? (draft[n.key] as string)
                        : ""
                    }
                    onChange={(e) =>
                      setDraft({ ...draft, [n.key]: e.target.value })
                    }
                  />
                </Field>
              )}
            </fieldset>
          ))
        )}
        <DialogFooter>
          <Button
            variant="outline"
            disabled={action.isPending}
            onClick={onClose}
          >
            Cancel
          </Button>
          <Button
            disabled={!ready || invalid || action.isPending}
            onClick={async () => {
              try {
                await action.mutateAsync({
                  path: `/lists/${list.id}/settings`,
                  method: "PATCH",
                  body: Object.fromEntries(
                    notificationNames.map((n) => [n.key, draft[n.key] ?? null]),
                  ),
                });
                onClose();
              } catch {
                /* displayed */
              }
            }}
          >
            {action.isPending ? "Saving…" : "Save settings"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Lists({
  brandId,
  lists,
  createRequest = false,
  onCreatedRequest,
}: {
  brandId: number;
  lists: SubscriberList[];
  createRequest?: boolean;
  onCreatedRequest?: () => void;
}) {
  const [listSearch, setListSearch] = useState("");
  const visibleLists = lists.filter((list) =>
    list.name.toLowerCase().includes(listSearch.trim().toLowerCase()),
  );
  const [notifications, setNotifications] = useState<SubscriberList | null>(
    null,
  );
  const [editing, setEditing] = useState<SubscriberList | null>(null);
  const [deleting, setDeleting] = useState<SubscriberList | null>(null);
  const [form, setForm] = useState<SubscriberList | null>(null);
  const action = useSubscriberAction(brandId);
  useEffect(() => {
    if (createRequest) {
      setEditing({ id: 0, name: "", opt_in: "double", uuid: "" });
      onCreatedRequest?.();
    }
  }, [createRequest, onCreatedRequest]);
  return (
    <section className="space-y-4">
      <div className="flex justify-between">
        <div>
          <h2 className="section-heading text-lg font-semibold">Lists</h2>
          <p className="page-subtitle text-sm text-muted-foreground mt-1">
            Organize audiences and manage subscription preferences.
          </p>
        </div>
        <Button
          onClick={() => {
            action.reset();
            setEditing({ id: 0, name: "", opt_in: "double", uuid: "" });
          }}
        >
          <Plus size={16} />
          New list
        </Button>
      </div>
      <div className="toolbar flex items-center gap-3">
        <div className="relative max-w-sm w-full">
          <Search
            size={16}
            className="absolute left-3 top-3 text-muted-foreground"
          />
          <Input
            aria-label="Search lists"
            className="toolbar-search pl-9"
            placeholder="Search lists…"
            value={listSearch}
            onChange={(e) => setListSearch(e.target.value)}
          />
        </div>
        <span className="text-sm text-muted-foreground">
          {visibleLists.length} {visibleLists.length === 1 ? "list" : "lists"}
        </span>
      </div>
      {!lists.length ? (
        <p className="text-sm text-muted-foreground">
          No lists yet. Create a list before adding subscribers.
        </p>
      ) : (
        <div className="table-card rounded-lg border overflow-x-auto">
          <table className={tableClass}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Opt-in</th>
                <th>Active</th>
                <th>Subscribed</th>
                <th>Unsubscribed</th>
                <th>Bounced</th>
                <th>Complained</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {!visibleLists.length && (
                <tr>
                  <td colSpan={8} className="empty-panel">
                    No lists match your search.
                  </td>
                </tr>
              )}
              {visibleLists.map((l) => (
                <tr key={l.id}>
                  <td>
                    <div className="flex items-center gap-3">
                      <span className="avatar">
                        <ListIcon size={17} />
                      </span>
                      <span className="row-title font-medium">{l.name}</span>
                    </div>
                  </td>
                  <td>
                    <Badge variant="outline">{l.opt_in} opt-in</Badge>
                  </td>
                  <td>{l.active?.toLocaleString() ?? "Unavailable"}</td>
                  <td>{l.subscribed?.toLocaleString() ?? "Unavailable"}</td>
                  <td>{l.unsubscribed?.toLocaleString() ?? "Unavailable"}</td>
                  <td>{l.bounced?.toLocaleString() ?? "Unavailable"}</td>
                  <td>{l.complained?.toLocaleString() ?? "Unavailable"}</td>
                  <td>
                    <DropdownMenu.Root>
                      <DropdownMenu.Trigger asChild>
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={`Actions for ${l.name}`}
                        >
                          <MoreHorizontal size={18} />
                        </Button>
                      </DropdownMenu.Trigger>
                      <DropdownMenu.Portal>
                        <DropdownMenu.Content
                          className="dropdown-content"
                          align="end"
                          sideOffset={6}
                        >
                          <DropdownMenu.Label className="dropdown-label">
                            List actions
                          </DropdownMenu.Label>
                          <DropdownMenu.Item
                            onSelect={() => {
                              action.reset();
                              setEditing({ ...l });
                            }}
                          >
                            Edit list
                          </DropdownMenu.Item>
                          <DropdownMenu.Item onSelect={() => setForm(l)}>
                            Subscribe form
                          </DropdownMenu.Item>
                          <DropdownMenu.Item
                            onSelect={() => setNotifications(l)}
                          >
                            Messages and pages
                          </DropdownMenu.Item>
                          <DropdownMenu.Separator className="dropdown-separator" />
                          <DropdownMenu.Item
                            className="text-destructive"
                            onSelect={() => {
                              action.reset();
                              setDeleting(l);
                            }}
                          >
                            Delete list
                          </DropdownMenu.Item>
                        </DropdownMenu.Content>
                      </DropdownMenu.Portal>
                    </DropdownMenu.Root>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <p className="text-xs text-muted-foreground">
        Counters come from preaggregated list totals. Subscribed and
        unsubscribed count transitions; active is the current membership count.
      </p>
      <Dialog
        open={!!editing}
        onOpenChange={(open) => !open && setEditing(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{editing?.id ? "Edit list" : "New list"}</DialogTitle>
          </DialogHeader>
          {editing && (
            <>
              <Field label="Name">
                <Input
                  value={editing.name}
                  onChange={(e) =>
                    setEditing({ ...editing, name: e.target.value })
                  }
                />
              </Field>
              <Field label="Opt-in">
                <SelectField
                  value={editing.opt_in}
                  onValueChange={(value) =>
                    setEditing({ ...editing, opt_in: value })
                  }
                  options={[
                    { value: "single", label: "Single opt-in" },
                    { value: "double", label: "Double opt-in" },
                  ]}
                />
              </Field>
              <p className="text-sm text-muted-foreground">
                Double opt-in requires email confirmation for new subscriptions.
              </p>
            </>
          )}
          <ErrorMessage error={action.error} />
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditing(null)}>
              Cancel
            </Button>
            <Button
              disabled={!editing?.name.trim() || action.isPending}
              onClick={async () => {
                if (!editing) return;
                try {
                  await action.mutateAsync({
                    path: editing.id ? `/lists/${editing.id}` : "/lists",
                    method: editing.id ? "PATCH" : "POST",
                    body: { name: editing.name.trim(), opt_in: editing.opt_in },
                  });
                  setEditing(null);
                } catch {
                  /* displayed */
                }
              }}
            >
              Save list
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog
        open={!!deleting}
        onOpenChange={(open) => !open && setDeleting(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete {deleting?.name}?</DialogTitle>
          </DialogHeader>
          <p className="text-sm">
            Deletes the list, its memberships and form. Subscriber profiles
            remain in the brand.
          </p>
          <ErrorMessage error={action.error} />
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleting(null)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={action.isPending}
              onClick={async () => {
                try {
                  await action.mutateAsync({
                    path: `/lists/${deleting?.id}`,
                    method: "DELETE",
                  });
                  setDeleting(null);
                } catch {
                  /* displayed */
                }
              }}
            >
              Delete list
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      {notifications && (
        <ListNotificationsDialog
          key={notifications.id}
          brandId={brandId}
          list={notifications}
          onClose={() => setNotifications(null)}
        />
      )}
      <Dialog open={!!form} onOpenChange={(open) => !open && setForm(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{form?.name} subscribe form</DialogTitle>
          </DialogHeader>
          {form && <ListForm brandId={brandId} list={form} />}
        </DialogContent>
      </Dialog>
    </section>
  );
}

function SubscribersWorkspace({ brandId }: { brandId: number }) {
  const [searchParams, setSearchParams] = useSearchParams();
  const [createList, setCreateList] = useState(false);
  const [tab, setTab] = useState("subscribers");
  const [after, setAfter] = useState(0);
  const [history, setHistory] = useState<number[]>([]);
  const [q, setQ] = useState("");
  const [list, setList] = useState(0);
  const [status, setStatus] = useState("");
  const [membershipStatus, setMembershipStatus] = useState("");
  const [profile, setProfile] = useState<Subscriber | null | undefined>(
    undefined,
  );
  const [showCreated, setShowCreated] = useState(false);
  const [showConsent, setShowConsent] = useState(true);
  const [lookup, setLookup] = useState("");
  const [lookupError, setLookupError] = useState<unknown>();
  const [lookupBusy, setLookupBusy] = useState(false);
  useEffect(() => {
    const request = searchParams.get("new");
    if (request === "subscriber" || request === "contact") setProfile(null);
    if (request === "list") {
      setTab("lists");
      setCreateList(true);
    }
    if (request) {
      const next = new URLSearchParams(searchParams);
      next.delete("new");
      setSearchParams(next, { replace: true });
    }
  }, [searchParams, setSearchParams]);
  const lists = useQuery({
    queryKey: ["subscribers", brandId, "lists"],
    queryFn: ({ signal }) =>
      allPages<SubscriberList>("/lists", brandId, signal),
  });
  const fields = useSubscribersQuery<CustomField[]>(
    brandId,
    "fields",
    "/custom-fields",
  );
  const params = new URLSearchParams({ after: String(after), limit: "100" });
  if (q.trim()) params.set("q", q.trim());
  if (list) params.set("list_id", String(list));
  if (status) params.set("status", status);
  if (list && membershipStatus)
    params.set("membership_status", membershipStatus);
  const subscribers = useSubscribersQuery<Subscriber[]>(
    brandId,
    "subscribers",
    `/subscribers?${params}`,
  );
  const resetPage = () => {
    setAfter(0);
    setHistory([]);
  };
  return (
    <div className="space-y-6">
      <div className="page-header flex flex-wrap items-end justify-between gap-2">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">
            Subscribers & lists
          </h1>
          <p className="page-subtitle text-sm text-muted-foreground mt-1">
            Manage subscriber profiles, consent and audience memberships.
          </p>
        </div>
        <Button onClick={() => setProfile(null)}>
          <Plus size={16} />
          New subscriber
        </Button>
      </div>
      <div className="page-tabs" role="tablist" aria-label="Subscriber tools">
        {[
          ["subscribers", "Subscribers"],
          ["lists", "Lists"],
          ["import", "Import / export"],
          ["bulk", "Mass actions"],
          ["fields", "Custom fields"],
          ["housekeeping", "Housekeeping"],
        ].map(([value, label]) => (
          <Button
            key={value}
            role="tab"
            aria-selected={tab === value}
            variant={tab === value ? "default" : "ghost"}
            size="sm"
            onClick={() => setTab(value)}
          >
            {label}
          </Button>
        ))}
      </div>
      <ErrorMessage error={lists.error || fields.error} />
      {tab === "subscribers" && (
        <section className="space-y-4">
          <div className="table-card">
            <div className="table-toolbar toolbar grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
              <Field label="Email prefix">
                <Input
                  placeholder="Search by email prefix"
                  className="toolbar-search"
                  value={q}
                  onChange={(e) => {
                    setQ(e.target.value);
                    resetPage();
                  }}
                />
              </Field>
              <Field label="List">
                <SelectField
                  value={String(list)}
                  onValueChange={(value) => {
                    setList(Number(value));
                    setMembershipStatus("");
                    resetPage();
                  }}
                  options={[
                    { value: "0", label: "All subscribers" },
                    ...(lists.data || []).map((l) => ({
                      value: String(l.id),
                      label: l.name,
                    })),
                  ]}
                />
              </Field>
              <Field label="Subscriber status">
                <SelectField
                  value={status}
                  onValueChange={(value) => {
                    setStatus(value);
                    resetPage();
                  }}
                  options={[
                    { value: "", label: "All statuses" },
                    ...statuses.map((s) => ({ value: s, label: s })),
                  ]}
                />
              </Field>
              <Field label="Membership status">
                <SelectField
                  disabled={!list}
                  value={membershipStatus}
                  onValueChange={(value) => {
                    setMembershipStatus(value);
                    resetPage();
                  }}
                  options={[
                    { value: "", label: "All memberships" },
                    ...["subscribed", "unsubscribed", "unconfirmed"].map(
                      (s) => ({ value: s, label: s }),
                    ),
                  ]}
                />
              </Field>
            </div>
            <div className="table-toolbar flex flex-wrap items-center justify-between gap-3">
              <div className="flex gap-3 w-full max-w-xl">
                <Input
                  type="email"
                  aria-label="Find subscriber by exact email"
                  placeholder="Find exact email address"
                  value={lookup}
                  onChange={(e) => setLookup(e.target.value)}
                />
                <Button
                  variant="outline"
                  disabled={!lookup.trim() || lookupBusy}
                  onClick={async () => {
                    setLookupBusy(true);
                    setLookupError(undefined);
                    try {
                      const v = await api<Subscriber>(
                        `/subscribers/by-email?email=${encodeURIComponent(lookup.trim())}`,
                        { brandId },
                      );
                      setProfile(v);
                    } catch (e) {
                      setLookupError(e);
                    } finally {
                      setLookupBusy(false);
                    }
                  }}
                >
                  {lookupBusy ? "Finding…" : "Find email"}
                </Button>
              </div>
              <DropdownMenu.Root>
                <DropdownMenu.Trigger asChild>
                  <Button variant="outline" size="sm">
                    <SlidersHorizontal size={15} />
                    View options
                  </Button>
                </DropdownMenu.Trigger>
                <DropdownMenu.Portal>
                  <DropdownMenu.Content
                    className="dropdown-content"
                    align="end"
                    sideOffset={6}
                  >
                    <DropdownMenu.Label className="dropdown-label">
                      Display columns
                    </DropdownMenu.Label>
                    <DropdownMenu.CheckboxItem
                      checked={showConsent}
                      onCheckedChange={setShowConsent}
                      onSelect={(event) => event.preventDefault()}
                    >
                      <span
                        className="flex size-4 items-center justify-center rounded-sm border border-input"
                        aria-hidden="true"
                      >
                        <DropdownMenu.ItemIndicator>
                          <Check size={12} />
                        </DropdownMenu.ItemIndicator>
                      </span>
                      Consent date
                    </DropdownMenu.CheckboxItem>
                    <DropdownMenu.CheckboxItem
                      checked={showCreated}
                      onCheckedChange={setShowCreated}
                      onSelect={(event) => event.preventDefault()}
                    >
                      <span
                        className="flex size-4 items-center justify-center rounded-sm border border-input"
                        aria-hidden="true"
                      >
                        <DropdownMenu.ItemIndicator>
                          <Check size={12} />
                        </DropdownMenu.ItemIndicator>
                      </span>
                      Created date
                    </DropdownMenu.CheckboxItem>
                  </DropdownMenu.Content>
                </DropdownMenu.Portal>
              </DropdownMenu.Root>
            </div>
            <ErrorMessage error={lookupError || subscribers.error} />
            {subscribers.isPending ? (
              <p role="status">Loading subscribers…</p>
            ) : !subscribers.data?.length ? (
              <div className="empty-panel rounded-lg p-10 text-center text-muted-foreground">
                <Users className="mx-auto mb-3" size={28} />
                <p className="row-title">No subscribers match these filters.</p>
                <p className="row-subtitle mt-1">
                  Try a different email prefix, list or status.
                </p>
              </div>
            ) : (
              <div className="overflow-x-auto">
                <table className={tableClass}>
                  <thead>
                    <tr>
                      <th>Subscriber</th>
                      <th>Subscriber status</th>
                      {showConsent && <th>Consent</th>}
                      {showCreated && <th>Created</th>}
                    </tr>
                  </thead>
                  <tbody>
                    {subscribers.data.map((s) => (
                      <tr
                        key={s.id}
                        tabIndex={0}
                        aria-label={`Open subscriber profile for ${s.name || s.email}`}
                        className="cursor-pointer focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
                        onClick={(event) => {
                          if (
                            event.target instanceof Element &&
                            event.target.closest(
                              "button, a, input, select, textarea, [role='checkbox'], [role='menuitem']",
                            )
                          )
                            return;
                          if (window.getSelection()?.toString()) return;
                          setProfile(s);
                        }}
                        onKeyDown={(event) => {
                          if (event.target !== event.currentTarget) return;
                          if (event.key === "Enter" || event.key === " ") {
                            event.preventDefault();
                            setProfile(s);
                          }
                        }}
                      >
                        <td>
                          <div className="flex items-center gap-3">
                            <span className="avatar" aria-hidden="true">
                              {subscriberInitials(s.name, s.email)}
                            </span>
                            <div>
                              <div className="row-title">
                                {s.name || "Unnamed subscriber"}
                              </div>
                              <div className="row-subtitle">{s.email}</div>
                            </div>
                          </div>
                        </td>
                        <td>
                          <SubscriberStatusBadge status={s.status} />
                        </td>
                        {showConsent && (
                          <td className="text-muted-foreground">
                            {s.gdpr_consent_at
                              ? new Date(s.gdpr_consent_at).toLocaleDateString()
                              : "Not recorded"}
                          </td>
                        )}
                        {showCreated && (
                          <td className="text-muted-foreground">
                            {new Date(s.created_at).toLocaleDateString()}
                          </td>
                        )}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            <div className="table-footer flex flex-wrap gap-3 justify-between items-center">
              <p className="text-xs text-muted-foreground">
                {subscribers.data?.length || 0} subscribers on this page. Status
                is separate from list opt-outs.
              </p>
              <div className="flex gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={!history.length || subscribers.isFetching}
                  onClick={() => {
                    setAfter(history[history.length - 1]);
                    setHistory(history.slice(0, -1));
                  }}
                >
                  Previous
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={
                    subscribers.data?.length !== 100 || subscribers.isFetching
                  }
                  onClick={() => {
                    setHistory([...history, after]);
                    setAfter(subscribers.data![99].id);
                  }}
                >
                  Next
                </Button>
              </div>
            </div>
          </div>
        </section>
      )}
      {tab === "lists" &&
        (lists.isPending ? (
          <p role="status">Loading lists…</p>
        ) : (
          <Lists
            brandId={brandId}
            lists={lists.data || []}
            createRequest={createList}
            onCreatedRequest={() => setCreateList(false)}
          />
        ))}
      {tab === "fields" &&
        (fields.isPending ? (
          <p role="status">Loading field definitions…</p>
        ) : (
          <CustomFields brandId={brandId} fields={fields.data || []} />
        ))}
      {tab === "import" && (
        <div className="space-y-6">
          <CSVImport
            brandId={brandId}
            lists={lists.data || []}
            fields={fields.data || []}
          />
          <ExportSubscribers brandId={brandId} lists={lists.data || []} />
        </div>
      )}
      {tab === "bulk" && (
        <MassOperations brandId={brandId} lists={lists.data || []} />
      )}
      {tab === "housekeeping" && <Housekeeping brandId={brandId} />}
      {profile !== undefined && (
        <SubscriberProfile
          key={profile?.id || "new"}
          brandId={brandId}
          subscriber={profile}
          fields={fields.data || []}
          lists={lists.data || []}
          onClose={() => setProfile(undefined)}
        />
      )}
    </div>
  );
}
export function SubscribersPage({ brandId }: { brandId: number }) {
  return brandId > 0 ? (
    <SubscribersWorkspace key={brandId} brandId={brandId} />
  ) : (
    <p>Select a brand to manage subscribers.</p>
  );
}
