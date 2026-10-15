import { useTableSort } from "../lib/use-table-sort";
import { PageTabs } from "../components/ui/page-tabs";
import { Field } from "../components/ui/field";
import { ConfirmDialog } from "../components/ui/confirm-dialog";
import { useDateTime } from "../lib/date-time";
import { SortableTableHead } from "../components/SortableTableHead";
import { SelectField } from "../components/ui/select";
import { Checkbox } from "../components/ui/checkbox";
import { useEffect, useState } from "react";
import {
  Plus,
  Search,
  UserMinus,
  Users,
  List as ListIcon,
  MoreHorizontal,
  Pencil,
  Trash2,
} from "lucide-react";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { Link, useSearchParams } from "react-router-dom";
import { Suppressions } from "../components/settings/Suppressions";
import { useQuery } from "@tanstack/react-query";
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
  type SubscriberList,
  type CustomField,
  type Subscriber,
  ErrorMessage,
  tableClass,
  useSubscriberAction,
  useCanWriteSubscribers,
  useSubscribersQuery,
} from "../components/subscribers/shared";
import {
  CSVImport,
  ExportSubscribers,
  Housekeeping,
  ListForm,
  MassOperations,
} from "../components/subscribers/Operations";

import { SubscriberProfile } from "./SubscriberProfile";
const statuses = ["active", "bounced", "complained", "blocklisted"];

function CustomFields({
  brandId,
  fields,
}: {
  brandId: number;
  fields: CustomField[];
}) {
  const canWrite = useCanWriteSubscribers(brandId);
  const [editing, setEditing] = useState<CustomField | null>(null);
  const sorting = useTableSort<keyof CustomField>(null);
  const sortedFields = sorting.sort(fields, (field, key) => field[key]);
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
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold">Custom fields</h2>
          <p className="text-sm text-muted-foreground">
            Filterable fields create database indexes for segment rules.
          </p>
        </div>
        <Button
          variant="outline"
          disabled={!canWrite}
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
                <SortableTableHead label="Label" {...sorting.header("label")} />
                <SortableTableHead label="Key" {...sorting.header("key")} />
                <SortableTableHead label="Type" {...sorting.header("type")} />
                <SortableTableHead label="Filterable" {...sorting.header("is_filterable")} />
                <th></th>
              </tr>
            </thead>
            <tbody>
              {sortedFields.map((f) => (
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
                      variant="default"
                      size="sm"
                      disabled={!canWrite}
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
              disabled={
                !canWrite || !editing?.key || !editing.label || action.isPending
              }
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
  const canWrite = useCanWriteSubscribers(brandId);
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
          <DialogTitle>{list.name}: subscription emails & pages</DialogTitle>
        </DialogHeader>
        <p className="text-sm text-muted-foreground">
          Defaults flow from Postbox defaults to brand defaults to this list's
          overrides. Customize only the messages that should differ for this
          list. Welcome emails are sent after subscribed consent. Confirmation
          emails support {"{{ confirmation_url }}"}.
        </p>
        <Link
          className="text-sm text-primary underline"
          to={`/brands/${brandId}/settings/subscriptions`}
          onClick={onClose}
        >
          Manage brand subscription defaults
        </Link>
        {!canWrite && (
          <p className="text-sm text-muted-foreground">
            You have read-only access to this list.
          </p>
        )}
        <ErrorMessage error={settings.error || action.error} />
        {settings.isPending ? (
          <p role="status">Loading settings…</p>
        ) : (
          ready &&
          notificationNames.map((n) => (
            <fieldset
              key={n.key}
              disabled={!canWrite}
              className="border rounded-lg p-4 space-y-3"
            >
              <legend className="font-medium text-sm px-1">{n.label}</legend>
              <label className="flex gap-2 text-sm items-center">
                <Checkbox
                  disabled={!canWrite}
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
                      : "Inherited from brand defaults or Postbox defaults."
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
            {canWrite ? "Cancel" : "Close"}
          </Button>
          <Button
            disabled={!canWrite || !ready || invalid || action.isPending}
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
  const canWrite = useCanWriteSubscribers(brandId);
  const [listSearch, setListSearch] = useState("");
  const sorting = useTableSort<keyof SubscriberList>(null);
  const sortedLists = sorting.sort(lists, (list, key) => list[key]);
  const visibleLists = sortedLists.filter((list) =>
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
    if (createRequest && canWrite) {
      setEditing({ id: 0, name: "", opt_in: "double", uuid: "" });
      onCreatedRequest?.();
    }
  }, [createRequest, onCreatedRequest, canWrite]);
  return (
    <section className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="section-heading text-lg font-semibold">Lists</h2>
          <p className="page-subtitle text-sm text-muted-foreground mt-1">
            Manage each list’s opt-in mode, subscribe form, and subscription
            overrides.
          </p>
        </div>
        <Button
          disabled={!canWrite}
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
                <SortableTableHead label="Name" {...sorting.header("name")} />
                <SortableTableHead label="Opt-in" {...sorting.header("opt_in")} />
                <SortableTableHead label="Active" {...sorting.header("active")} />
                <SortableTableHead label="Subscribed" {...sorting.header("subscribed")} />
                <SortableTableHead label="Unsubscribed" {...sorting.header("unsubscribed")} />
                <SortableTableHead label="Bounced" {...sorting.header("bounced")} />
                <SortableTableHead label="Complained" {...sorting.header("complained")} />
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {!visibleLists.length && (
                <tr>
                  <td colSpan={8}>
                    <div className="empty-panel">No lists match your search.</div>
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
                      <div>
                        <span className="row-title font-medium">{l.name}</span>
                        <p className="row-subtitle">
                          API ID: <code>{l.id}</code>
                        </p>
                      </div>
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
                    <div className="flex items-center gap-1">
                      <Button
                        size="icon"
                        variant="outline"
                        className="action-edit"
                        disabled={!canWrite}
                        aria-label={`Edit list ${l.name}`}
                        title="Edit list"
                        onClick={() => {
                          action.reset();
                          setEditing({ ...l });
                        }}
                      >
                        <Pencil size={16} aria-hidden="true" />
                      </Button>
                      <Button
                        size="icon"
                        variant="outline"
                        className="action-delete"
                        disabled={!canWrite}
                        aria-label={`Delete list ${l.name}`}
                        title="Delete list"
                        onClick={() => {
                          action.reset();
                          setDeleting(l);
                        }}
                      >
                        <Trash2 size={16} aria-hidden="true" />
                      </Button>
                      <DropdownMenu.Root>
                        <DropdownMenu.Trigger asChild>
                          <Button
                            size="icon"
                            variant="ghost"
                            aria-label={`More actions for ${l.name}`}
                            title="More actions"
                          >
                            <MoreHorizontal size={18} aria-hidden="true" />
                          </Button>
                        </DropdownMenu.Trigger>
                        <DropdownMenu.Portal>
                          <DropdownMenu.Content
                            className="dropdown-content"
                            align="end"
                            sideOffset={6}
                          >
                            <DropdownMenu.Label className="dropdown-label">
                              More actions
                            </DropdownMenu.Label>
                            <DropdownMenu.Item onSelect={() => setForm(l)}>
                              Subscribe form
                            </DropdownMenu.Item>
                            <DropdownMenu.Item
                              onSelect={() => setNotifications(l)}
                            >
                              Subscription emails & pages
                            </DropdownMenu.Item>
                          </DropdownMenu.Content>
                        </DropdownMenu.Portal>
                      </DropdownMenu.Root>
                    </div>
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
              disabled={!canWrite || !editing?.name.trim() || action.isPending}
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
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={`Delete ${deleting?.name}?`}
        description={
          <>
            Deletes the list, its memberships and form, and removes it from
            draft campaign audiences. Subscriber profiles remain in the brand.
            Lists used by scheduled or active campaigns cannot be deleted.
          </>
        }
        error={action.error}
        pending={action.isPending}
        pendingLabel="Deleting…"
        confirmLabel="Delete list"
        confirmDisabled={!canWrite || action.isPending}
        onConfirm={async () => {
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
      />
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
  const canWrite = useCanWriteSubscribers(brandId);
  const { formatDateTime } = useDateTime();
  const [searchParams, setSearchParams] = useSearchParams();
  const [createList, setCreateList] = useState(false);
  const requestedTab = searchParams.get("tab") || "subscribers";
  const tab = [
    "subscribers",
    "lists",
    "import",
    "bulk",
    "fields",
    "housekeeping",
    "suppressions",
  ].includes(requestedTab)
    ? requestedTab
    : "subscribers";
  const setTab = (value: string) => {
    setSearchParams((current) => {
      const next = new URLSearchParams(current);
      next.set("tab", value);
      return next;
    });
  };
  const [after, setAfter] = useState(0);
  const [sort, setSort] = useState("id");
  const [direction, setDirection] = useState<"asc" | "desc">("asc");
  const [history, setHistory] = useState<number[]>([]);
  const [q, setQ] = useState("");
  const [name, setName] = useState("");
  const [list, setList] = useState(0);
  const [status, setStatus] = useState("");
  const [membershipStatus, setMembershipStatus] = useState("");
  const [profile, setProfile] = useState<Subscriber | null | undefined>(
    undefined,
  );
  const [profileAction, setProfileAction] = useState<
    "edit" | "delete" | "unsubscribe"
  >("edit");
  const openProfile = (
    subscriber: Subscriber | null,
    action: typeof profileAction = "edit",
  ) => {
    setProfileAction(action);
    setProfile(subscriber);
  };
  useEffect(() => {
    const request = searchParams.get("new");
    if (canWrite && (request === "subscriber" || request === "contact"))
      openProfile(null);
    if (canWrite && request === "list") {
      setCreateList(true);
    }
    if (request) {
      const next = new URLSearchParams(searchParams);
      next.delete("new");
      if (canWrite && request === "list") next.set("tab", "lists");
      setSearchParams(next, { replace: true });
    }
  }, [searchParams, setSearchParams, canWrite]);
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
  params.set("sort", sort);
  params.set("direction", direction);
  if (q.trim()) params.set("q", q.trim());
  if (name.trim()) params.set("name", name.trim());
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
  const sortBy = (column: string) => {
    setDirection(sort === column && direction === "asc" ? "desc" : "asc");
    setSort(column);
    resetPage();
  };
  const sortableHead = (label: string, column: string) => (
    <SortableTableHead
      label={label}
      active={sort === column}
      direction={direction}
      onSort={() => sortBy(column)}
    />
  );
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
        <Button disabled={!canWrite} onClick={() => openProfile(null)}>
          <Plus size={16} />
          New subscriber
        </Button>
      </div>
      <PageTabs
        value={tab}
        onValueChange={setTab}
        label="Subscriber tools"
        items={[
          { value: "subscribers", label: "Subscribers" },
          { value: "lists", label: "Lists" },
          { value: "import", label: "Import / export" },
          { value: "bulk", label: "Mass actions", disabled: !canWrite },
          { value: "fields", label: "Custom fields" },
          { value: "housekeeping", label: "Housekeeping", disabled: !canWrite },
          { value: "suppressions", label: "Suppressions" },
        ]}
      >
      <ErrorMessage error={lists.error || fields.error} />
      {tab === "subscribers" && (
        <section className="space-y-4">
          <div className="table-card">
            <div className="table-toolbar toolbar grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
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
            <ErrorMessage error={subscribers.error} />
            <div className="overflow-x-auto">
                <table className={`${tableClass} whitespace-nowrap`}>
                  <thead>
                    <tr>
                      {sortableHead("Name", "name")}
                      {sortableHead("Email", "email")}
                      {sortableHead("Last activity", "last_activity")}
                      {sortableHead("Created at", "created_at")}
                      <th>
                        <span className="sr-only">Actions</span>
                      </th>
                    </tr>
                    <tr>
                      <td className="min-w-48 px-2 pb-2">
                        <Input
                          aria-label="Search subscriber names"
                          placeholder="Search names"
                          value={name}
                          onChange={(e) => {
                            setName(e.target.value);
                            resetPage();
                          }}
                        />
                      </td>
                      <td className="min-w-56 px-2 pb-2">
                        <Input
                          aria-label="Search subscriber emails"
                          placeholder="Search emails"
                          value={q}
                          onChange={(e) => {
                            setQ(e.target.value);
                            resetPage();
                          }}
                        />
                      </td>
                      <td colSpan={3} />
                    </tr>
                  </thead>
                  <tbody>
                    {subscribers.isPending ? (
                      <tr><td colSpan={5} role="status">Loading subscribers…</td></tr>
                    ) : !subscribers.data?.length ? (
                      <tr><td colSpan={5} className="p-10 text-center text-muted-foreground">
                        <Users className="mx-auto mb-3" size={28} />
                        <p className="row-title">No subscribers match these filters.</p>
                        <p className="row-subtitle mt-1">Try a different name, email, list or status.</p>
                      </td></tr>
                    ) : subscribers.data.map((s) => (
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
                          openProfile(s);
                        }}
                        onKeyDown={(event) => {
                          if (event.target !== event.currentTarget) return;
                          if (event.key === "Enter" || event.key === " ") {
                            event.preventDefault();
                            openProfile(s);
                          }
                        }}
                      >
                        <td>
                          <span
                            className="row-title block max-w-[20rem] truncate"
                            title={s.name || "Unnamed subscriber"}
                          >
                            {s.name || "Unnamed subscriber"}
                          </span>
                        </td>
                        <td className="text-muted-foreground">
                          <span
                            className="block max-w-[24rem] truncate"
                            title={s.email}
                          >
                            {s.email}
                          </span>
                        </td>
                        <td>
                          <p className="text-sm capitalize">
                            {s.last_activity_kind ||
                              (s.updated_at > s.created_at
                                ? "Updated"
                                : "Created")}
                          </p>
                          <time
                            className="text-xs text-muted-foreground"
                            dateTime={new Date(
                              s.last_activity_at ||
                                s.updated_at ||
                                s.created_at,
                            ).toISOString()}
                          >
                            {formatDateTime(
                              s.last_activity_at ||
                                s.updated_at ||
                                s.created_at,
                            )}
                          </time>
                        </td>
                        <td>
                          <time
                            className="text-xs text-muted-foreground"
                            dateTime={new Date(s.created_at).toISOString()}
                          >
                            {formatDateTime(s.created_at)}
                          </time>
                        </td>
                        <td>
                          <div className="campaign-row-actions">
                            <Button
                              variant="ghost"
                              size="icon"
                              aria-label={`${canWrite ? "Edit" : "View"} ${s.email}`}
                              title={canWrite ? "Edit" : "View"}
                              onClick={() => openProfile(s)}
                            >
                              <Pencil size={16} aria-hidden="true" />
                            </Button>
                            <Button
                              variant="ghost"
                              size="icon"
                              className="campaign-delete-action"
                              disabled={!canWrite}
                              aria-label={`Delete ${s.email}`}
                              title="Delete"
                              onClick={() => openProfile(s, "delete")}
                            >
                              <Trash2 size={16} aria-hidden="true" />
                            </Button>
                            <Button
                              variant="ghost"
                              size="icon"
                              disabled={!canWrite}
                              aria-label={`Unsubscribe ${s.email}`}
                              title="Unsubscribe"
                              onClick={() => openProfile(s, "unsubscribe")}
                            >
                              <UserMinus size={16} aria-hidden="true" />
                            </Button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
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
          {canWrite && (
            <CSVImport
              brandId={brandId}
              lists={lists.data || []}
              fields={fields.data || []}
            />
          )}
          <ExportSubscribers brandId={brandId} lists={lists.data || []} />
        </div>
      )}
      {!canWrite && ["bulk", "housekeeping"].includes(tab) && (
        <p className="text-sm text-muted-foreground">
          Brand write access is required for this tool.
        </p>
      )}
      {tab === "bulk" && canWrite && (
        <MassOperations brandId={brandId} lists={lists.data || []} />
      )}
      {tab === "housekeeping" && canWrite && <Housekeeping brandId={brandId} />}
      {tab === "suppressions" && (
        <Suppressions brandId={brandId} scope="brand" />
      )}
      </PageTabs>
      {profile !== undefined && (
        <SubscriberProfile
          key={`${profile?.id || "new"}-${profileAction}`}
          initialAction={profileAction}
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
