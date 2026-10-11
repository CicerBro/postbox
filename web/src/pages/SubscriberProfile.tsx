import { SelectField } from "../components/ui/select";
import { Checkbox } from "../components/ui/checkbox";
import { useEffect, useState } from "react";
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
type Membership = {
  list_id: number;
  status: string;
  created_at: number;
  updated_at: number;
};
type Activity = { id: number; kind: string; at: number; detail: string };
const statuses = ["active", "bounced", "complained", "blocklisted"];

export function SubscriberProfile({
  brandId,
  subscriber,
  fields,
  lists,
  onClose,
  initialAction = "edit",
}: {
  brandId: number;
  subscriber: Subscriber | null;
  fields: CustomField[];
  lists: SubscriberList[];
  onClose: () => void;
  initialAction?: "edit" | "delete" | "unsubscribe";
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
  const [erase, setErase] = useState(initialAction === "delete");
  const [eraseEmail, setEraseEmail] = useState("");
  const [unsubscribeList, setUnsubscribeList] = useState<number | null>(initialAction === "unsubscribe" ? 0 : null);
  const [activityCursor, setActivityCursor] = useState(0);
  const closeErase = () => {
    setErase(false);
    if (initialAction === "delete") onClose();
  };
  const closeUnsubscribe = () => {
    setUnsubscribeList(null);
    if (initialAction === "unsubscribe") onClose();
  };
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
      <Dialog open={initialAction === "edit"} onOpenChange={(open) => !open && onClose()}>
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
            {subscriber?.marketing_unsubscribed_at && (
              <p className="text-sm text-destructive" role="status">
                Unsubscribed from all marketing for this brand since{" "}
                {new Date(subscriber.marketing_unsubscribed_at).toLocaleString()}.
                List additions and profile edits will not restore eligibility.
              </p>
            )}
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
      <Dialog open={erase} onOpenChange={(open) => !open && closeErase()}>
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
              onClick={closeErase}
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
        onOpenChange={(open) => !open && closeUnsubscribe()}
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
            <Button variant="outline" onClick={closeUnsubscribe}>
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
                  closeUnsubscribe();
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
