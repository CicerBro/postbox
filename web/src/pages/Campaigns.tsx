import { useEffect, useRef, useState } from "react";
import { Link, Navigate, useNavigate, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useApp } from "../lib/app-context";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Checkbox } from "../components/ui/checkbox";
import { SelectField } from "../components/ui/select";
import { Textarea } from "../components/ui/textarea";
import { Plus, Search, ArrowUpRight, Mail, Users, Send } from "lucide-react";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "../components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "../components/ui/dialog";
import { SortableTableHead } from "../components/SortableTableHead";
import { EmailEditor } from "../components/EmailEditor";
import {
  StatusBadge,
  contentExcerpt,
  allRows,
  bytes,
  Campaign,
  ErrorMessage,
  initialHTML,
  Named,
  number,
  tableClass,
  Target,
  Template,
} from "./campaign-shared";

type Issue = { code: string; message: string; tag?: string };
type Checklist = {
  recipients: number;
  message_bytes: number;
  upload_bytes: number;
  duration_seconds: number;
  quota: {
    MaxSendRate?: number;
    Max24HourSend?: number;
    SentLast24Hours?: number;
  };
  fits_daily_quota: boolean;
  checks: {
    ready: boolean;
    errors: Issue[];
    warnings: Issue[];
    links: {
      url: string;
      status: string;
      http_status?: number;
      message?: string;
      personalized: boolean;
    }[];
    html_bytes: number;
    html_clipping: boolean;
    suppressed: number;
    inactive: number;
    skipped: number;
    remaining_daily_quota: number;
    user_remaining_daily_quota?: number;
    sample_recipient?: number;
  };
};
export function CampaignsPage({ brandId }: { brandId: number }) {
  const [offset, setOffset] = useState(0);
  const [search, setSearch] = useState(""),
    [filter, setFilter] = useState("all");
  const [sort, setSort] = useState("priority"),
    [direction, setDirection] = useState<"asc" | "desc">("desc");
  useEffect(() => {
    setSearch("");
    setFilter("all");
    setOffset(0);
    setSort("priority");
    setDirection("desc");
  }, [brandId]);
  const query = useQuery({
    queryKey: ["campaigns", brandId, offset, sort, direction, filter, search],
    queryFn: () => {
      const params = new URLSearchParams({
        offset: String(offset), limit: "50", sort, direction, status: filter, search,
      });
      return api<Campaign[]>(`/campaigns?${params}`, { brandId });
    },
    enabled: brandId > 0,
  });
  function sortBy(column: string) {
    setDirection(sort === column && direction === "asc" ? "desc" : "asc");
    setSort(column);
    setOffset(0);
  }
  function sortHeader(label: string, column: string) {
    return (
      <SortableTableHead
        label={label}
        active={sort === column}
        direction={direction}
        onSort={() => sortBy(column)}
      />
    );
  }
  return (
    <div className="space-y-6">
      <div className="page-header flex flex-wrap items-end justify-between gap-2">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Campaigns</h1>
          <p className="page-subtitle text-sm text-muted-foreground">
            Create newsletters and follow each delivery.
          </p>
        </div>
        <Button asChild>
          <Link to="/campaigns/new">
            <Plus size={16} /> New campaign
          </Link>
        </Button>
      </div>
      <div className="page-tabs" aria-label="Filter campaigns">
        {["all", "draft", "scheduled", "sending", "sent"].map((tab) => (
          <button
            key={tab}
            type="button"
            className={filter === tab ? "active" : ""}
            aria-pressed={filter === tab}
            onClick={() => {
              setFilter(tab);
              setOffset(0);
            }}
          >
            {tab === "all"
              ? "All campaigns"
              : tab.charAt(0).toUpperCase() + tab.slice(1)}
          </button>
        ))}
      </div>
      <ErrorMessage error={query.error} />
      {query.isPending ? (
        <p role="status">Loading campaigns…</p>
      ) : (
        <div className="table-card">
          <div className="table-toolbar">
            <div>
              <h2 className="section-heading">Campaign overview</h2>
              <p className="row-subtitle">
                {query.data?.length ?? 0} campaigns on this page
              </p>
            </div>
            <div className="toolbar-search">
              <Search size={16} aria-hidden="true" />
              <Input
                aria-label="Search campaigns"
                placeholder="Search campaigns…"
                value={search}
                onChange={(e) => {
                  setSearch(e.target.value);
                  setOffset(0);
                }}
              />
            </div>
          </div>
          <div className="overflow-auto">
            <table className={tableClass}>
              <thead>
                <tr>
                  {sortHeader("Campaign", "subject")}
                  {sortHeader("Status", "status")}
                  {sortHeader("Created", "created_at")}
                  {sortHeader("Scheduled", "scheduled_at")}
                  <th>
                    <span className="sr-only">Report</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {query.data?.map((c) => (
                    <tr key={c.id}>
                      <td>
                        <Link className="row-title" to={`/campaigns/${c.id}${c.status === "sent" ? "/report" : ""}`}>
                          {c.subject}
                        </Link>
                        <p className="row-subtitle max-w-sm truncate">
                          {contentExcerpt(c.html) || `Campaign #${c.id}`}
                        </p>
                      </td>
                      <td>
                        <StatusBadge status={c.status} />
                      </td>
                      <td>{new Date(c.created_at).toLocaleString()}</td>
                      <td>
                        {c.scheduled_at
                          ? new Date(c.scheduled_at).toLocaleString()
                          : "Unscheduled"}
                      </td>
                      <td>
                        <Button variant="ghost" size="sm" asChild>
                          <Link to={`/campaigns/${c.id}/report`}>
                            Report <ArrowUpRight size={14} />
                          </Link>
                        </Button>
                      </td>
                    </tr>
                  ))}
              </tbody>
            </table>
            {!query.data?.length && (
              <div className="empty-panel">
                <Mail size={28} />
                <h3>No campaigns found</h3>
                <p>
                  {search || filter !== "all"
                    ? "Try another search or status."
                    : "Create your first campaign to reach your audience."}
                </p>
              </div>
            )}
          </div>
        </div>
      )}
      <div className="table-footer flex gap-2">
        <span className="row-subtitle mr-auto">Page {offset / 50 + 1}</span>
        <Button
          variant="outline"
          disabled={offset === 0}
          onClick={() => {
            setOffset(Math.max(0, offset - 50));
          }}
        >
          Previous
        </Button>
        <Button
          variant="outline"
          disabled={query.data?.length !== 50}
          onClick={() => {
            setOffset(offset + 50);
          }}
        >
          Next
        </Button>
      </div>
    </div>
  );
}
export function CampaignDetailPage({ brandId }: { brandId: number }) {
  const { id } = useParams();
  return (
    <CampaignDetail
      key={`${brandId}:${id ?? "new"}`}
      brandId={brandId}
      id={id}
    />
  );
}
function CampaignDetail({ brandId, id }: { brandId: number; id?: string }) {
  const routeID = id && id !== "new" ? Number(id) : 0,
    navigate = useNavigate(),
    client = useQueryClient(),
    { session } = useApp();
  const [draftID, setDraftID] = useState(routeID),
    [subject, setSubject] = useState(""),
    [html, setHtml] = useState(initialHTML),
    [text, setText] = useState(""),
    [inline, setInline] = useState(true),
    [targets, setTargets] = useState<Target[]>([]),
    [baseline, setBaseline] = useState(""),
    [error, setError] = useState<unknown>(null),
    [notice, setNotice] = useState(""),
    [schedule, setSchedule] = useState(""),
    [testEmails, setTestEmails] = useState(""),
    [confirm, setConfirm] = useState<
      "start" | "schedule" | "cancel" | "delete" | null
    >(null),
    [busy, setBusy] = useState(false);
  const hydrated = useRef(false);
  const campaign = useQuery({
    queryKey: ["campaign", brandId, routeID],
    queryFn: () => api<Campaign[]>(`/campaigns/${routeID}`, { brandId }),
    enabled: brandId > 0 && routeID > 0,
    refetchInterval: 5000,
  });
  const targetQuery = useQuery({
    queryKey: ["campaign-targets", brandId, routeID],
    queryFn: () => api<Target[]>(`/campaigns/${routeID}/targets`, { brandId }),
    enabled: brandId > 0 && routeID > 0,
  });
  const choices = useQuery({
    queryKey: ["campaign-choices", brandId],
    queryFn: async () => {
      const [lists, segments, templates] = await Promise.all([
        allRows<Named>("/lists", brandId),
        allRows<Named>("/segments", brandId),
        allRows<Template>("/templates", brandId),
      ]);
      return { lists, segments, templates };
    },
    enabled: brandId > 0,
  });
  const snapshot = JSON.stringify({ subject, html, text, inline, targets });
  const dirty = routeID === 0 ? true : snapshot !== baseline;
  useEffect(() => {
    if (hydrated.current || !campaign.data?.[0] || !targetQuery.data) return;
    const c = campaign.data[0],
      t = targetQuery.data;
    setSubject(c.subject);
    setHtml(c.html);
    setText(c.text ?? "");
    setInline(Boolean(c.inline_css));
    setTargets(t);
    setBaseline(
      JSON.stringify({
        subject: c.subject,
        html: c.html,
        text: c.text ?? "",
        inline: Boolean(c.inline_css),
        targets: t,
      }),
    );
    hydrated.current = true;
  }, [campaign.data, targetQuery.data]);
  const status = campaign.data?.[0]?.status ?? "draft",
    editable = status === "draft",
    owner = campaign.data?.[0]?.created_by;
  const canSend = session.admin || !routeID || owner === session.user_id;
  const checklist = useQuery({
    queryKey: ["campaign-checklist", brandId, draftID],
    queryFn: () =>
      api<Checklist>(`/campaigns/${draftID}/checklist`, { brandId }),
    enabled: false,
    retry: false,
  });
  const save = useMutation({
    mutationFn: async () => {
      if (!subject.trim() || !html.trim())
        throw new Error("Subject and HTML are required.");
      const result = await api<{ id: number }>(
        draftID ? `/campaigns/${draftID}` : "/campaigns",
        {
          method: draftID ? "PATCH" : "POST",
          brandId,
          body: { subject, html, text, inline_css: inline },
        },
      );
      setDraftID(result.id);
      await api(`/campaigns/${result.id}/targets`, {
        method: "PUT",
        brandId,
        body: targets,
      });
      return { id: result.id, basis: snapshot };
    },
    onSuccess: (result) => {
      const saved = result.id;
      setBaseline(result.basis);
      setNotice("Draft saved.");
      client.invalidateQueries({ queryKey: ["campaigns", brandId] });
      client.invalidateQueries({ queryKey: ["campaign-checklist", brandId] });
      if (!routeID) navigate(`/campaigns/${saved}`, { replace: true });
      else
        client.invalidateQueries({ queryKey: ["campaign", brandId, routeID] });
    },
    onError: setError,
  });
  async function action(action: string, body?: unknown) {
    setError(null);
    setNotice("");
    setBusy(true);
    try {
      const result = await api<{ id?: number }>(
        `/campaigns/${draftID}/${action}`,
        { method: "POST", brandId, body },
      );
      if (action === "duplicate" && result.id) {
        navigate(`/campaigns/${result.id}`);
        return;
      }
      setNotice(
        action === "test-send"
          ? "[Test] emails sent."
          : "Campaign action completed.",
      );
      setConfirm(null);
      await client.invalidateQueries({
        queryKey: ["campaign", brandId, routeID],
      });
      client.invalidateQueries({ queryKey: ["campaigns", brandId] });
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }
  async function preflight() {
    setError(null);
    if (dirty || !draftID) {
      setError(
        new Error("Save your draft and audience before running preflight."),
      );
      return;
    }
    const result = await checklist.refetch();
    if (result.error) setError(result.error);
  }
  async function sendingConfirmation(mode: "start" | "schedule") {
    setError(null);
    if (dirty) {
      setError(new Error("Save changes before sending."));
      return;
    }
    if (
      mode === "schedule" &&
      (!schedule || new Date(schedule).getTime() <= Date.now())
    ) {
      setError(new Error("Choose a future date and time."));
      return;
    }
    const result = await checklist.refetch();
    if (result.error) {
      setError(result.error);
      return;
    }
    if (!result.data?.checks.ready) {
      setError(new Error("Resolve the preflight errors before sending."));
      return;
    }
    setConfirm(mode);
  }
  async function confirmed() {
    if (confirm === "delete") {
      setBusy(true);
      try {
        await api(`/campaigns/${draftID}`, { method: "DELETE", brandId });
        client.invalidateQueries({ queryKey: ["campaigns", brandId] });
        navigate("/campaigns");
      } catch (e) {
        setError(e);
      } finally {
        setBusy(false);
      }
    } else if (confirm === "schedule")
      await action("schedule", {
        scheduled_at: new Date(schedule).getTime(),
        timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
      });
    else if (confirm) await action(confirm);
  }
  function targetMode(
    type: "list" | "segment",
    targetID: number,
    mode: string,
  ) {
    setTargets((t) => [
      ...t.filter((x) => x.target_type !== type || x.target_id !== targetID),
      ...(mode
        ? [
            {
              target_type: type,
              target_id: targetID,
              mode: mode as Target["mode"],
            },
          ]
        : []),
    ]);
  }
  if (routeID && (!Number.isSafeInteger(routeID) || routeID < 1))
    return <ErrorMessage error={new Error("Invalid campaign ID")} />;
  if (routeID && campaign.data?.[0]?.status === "sent")
    return <Navigate to={`/campaigns/${routeID}/report`} replace />;
  if (routeID && (campaign.isPending || targetQuery.isPending))
    return <p role="status">Loading campaign…</p>;
  if (routeID && (campaign.error || targetQuery.error))
    return <ErrorMessage error={campaign.error ?? targetQuery.error} />;
  if (routeID && !campaign.data?.length) return <p>Campaign not found.</p>;
  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <Link to="/campaigns" className="text-sm underline">
            Campaigns
          </Link>
          <h1 className="mt-2 text-2xl font-bold tracking-tight">
            {routeID ? subject || "Campaign" : "New campaign"}
          </h1>
          <div className="mt-2 flex items-center gap-3">
            <StatusBadge status={status} />
            <span className="row-subtitle">
              {dirty && editable ? "Unsaved changes" : "Saved campaign"}
            </span>
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          {draftID > 0 && (
            <>
              <Button asChild variant="outline">
                <Link to={`/campaigns/${draftID}/report`}>Report</Link>
              </Button>
              <Button
                variant="outline"
                disabled={busy}
                onClick={() => action("duplicate")}
              >
                Duplicate
              </Button>
            </>
          )}
          {editable && (
            <Button
              disabled={save.isPending || busy}
              onClick={() => {
                setError(null);
                setNotice("");
                save.mutate();
              }}
            >
              {save.isPending ? "Saving…" : "Save draft"}
            </Button>
          )}
        </div>
      </div>
      <ErrorMessage error={error ?? choices.error} />
      {notice && (
        <p role="status" className="rounded-md border p-3 text-sm">
          {notice}
        </p>
      )}
      {!canSend && (
        <p className="text-sm text-muted-foreground">
          Duplicate this campaign to send it from your account. Server
          permissions are enforced for every action.
        </p>
      )}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Mail size={18} /> Campaign content
          </CardTitle>
          <p className="row-subtitle">
            Craft the message your subscribers will receive.
          </p>
        </CardHeader>
        <CardContent className="space-y-4">
          <label className="block text-sm">
            Subject
            <Input
              value={subject}
              disabled={!editable}
              onChange={(e) => setSubject(e.target.value)}
            />
          </label>
          {editable && (
            <label htmlFor="campaign-template" className="block space-y-2 text-sm">
              Start from a template
              <SelectField
                id="campaign-template"
                value=""
                onValueChange={(value) => {
                  const t = choices.data?.templates.find((t) => t.id === Number(value));
                  if (t) setHtml(t.html);
                }}
                options={[
                  { value: "", label: "Choose a template to replace the current HTML" },
                  ...(choices.data?.templates.map((t) => ({ value: String(t.id), label: t.name })) ?? []),
                ]}
              />
            </label>
          )}
          <label htmlFor="campaign-inline-css" className="flex items-center gap-3 text-sm">
            <Checkbox
              id="campaign-inline-css"
              checked={inline}
              disabled={!editable}
              onCheckedChange={(checked) => setInline(checked === true)}
            />
            Inline CSS during rendering
          </label>
          <EmailEditor
            brandId={brandId}
            value={html}
            onChange={setHtml}
            readOnly={!editable}
            inlineCSS={inline}
            subject={subject}
            campaignId={draftID}
            text={text}
            audienceTargets={targets}
          />
          <details>
            <summary className="cursor-pointer text-sm">
              Plain-text override (optional)
            </summary>
            <Textarea
              aria-label="Plain text"
              value={text}
              disabled={!editable}
              onChange={(e) => setText(e.target.value)}
              className="mt-2"
            />
            <p className="text-sm text-muted-foreground">
              Leave blank to generate text from HTML.
            </p>
          </details>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Users size={18} /> Audience
          </CardTitle>
          <p className="row-subtitle">
            {targets.filter((t) => t.mode === "include").length} included
            audiences · {targets.filter((t) => t.mode === "exclude").length}{" "}
            exclusions
          </p>
        </CardHeader>
        <CardContent className="space-y-3">
          <p className="text-sm text-muted-foreground">
            Include lists or segments, then exclude any audience you want to
            omit. Recipients are deduplicated and inactive or suppressed
            subscribers are skipped.
          </p>
          {choices.isPending ? (
            <p role="status">Loading audiences…</p>
          ) : (
            <div className="grid gap-6 md:grid-cols-2">
              {(["list", "segment"] as const).map((type) => (
                <div key={type}>
                  <h3 className="mb-3 font-medium">
                    {type === "list" ? "Lists" : "Segments"}
                  </h3>
                  {choices.data?.[type === "list" ? "lists" : "segments"].map(
                    (row) => (
                      <div key={row.id} className="audience-option mb-2 flex flex-wrap items-center justify-between gap-3 rounded-md border p-3 text-sm">
                        <span className="font-medium">{row.name}</span>
                        <div className="flex gap-4">
                          {(["include", "exclude"] as const).map((mode) => (
                            <label key={mode} className="flex items-center gap-2">
                              <Checkbox
                                aria-label={`${mode === "include" ? "Include" : "Exclude"} ${row.name}`}
                                disabled={!editable}
                                checked={targets.some((t) => t.target_type === type && t.target_id === row.id && t.mode === mode)}
                                onCheckedChange={(checked) => targetMode(type, row.id, checked === true ? mode : "")}
                              />
                              {mode === "include" ? "Include" : "Exclude"}
                            </label>
                          ))}
                        </div>
                      </div>
                    ),
                  )}
                  {!choices.data?.[type === "list" ? "lists" : "segments"]
                    .length && (
                    <p className="text-sm text-muted-foreground">
                      No {type === "list" ? "lists" : "segments"} available.
                    </p>
                  )}
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
      {draftID > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Send size={18} /> Delivery
            </CardTitle>
            <p className="row-subtitle">
              Review your campaign, send a test, and choose when to deliver.
            </p>
          </CardHeader>
          <CardContent className="space-y-4">
            {editable && (
              <>
                <div className="flex flex-wrap gap-2">
                  <Button
                    variant="outline"
                    onClick={preflight}
                    disabled={checklist.isFetching || busy || dirty}
                  >
                    {checklist.isFetching ? "Checking…" : "Run preflight"}
                  </Button>
                  <Button
                    onClick={() => sendingConfirmation("start")}
                    disabled={busy || dirty || !canSend || checklist.isFetching}
                  >
                    Start sending
                  </Button>
                </div>
                <div className="flex flex-wrap items-end gap-2">
                  <label className="text-sm">
                    Schedule in{" "}
                    {Intl.DateTimeFormat().resolvedOptions().timeZone}
                    <Input
                      type="datetime-local"
                      value={schedule}
                      onChange={(e) => setSchedule(e.target.value)}
                    />
                  </label>
                  <Button
                    variant="outline"
                    onClick={() => sendingConfirmation("schedule")}
                    disabled={busy || dirty || !canSend || checklist.isFetching}
                  >
                    Schedule
                  </Button>
                </div>
              </>
            )}
            {["sending", "preparing"].includes(status) && (
              <Button
                variant="outline"
                disabled={busy}
                onClick={() => action("pause")}
              >
                Pause sending
              </Button>
            )}
            {status === "paused" && (
              <Button
                disabled={busy || !canSend}
                onClick={() => action("resume")}
              >
                Resume sending
              </Button>
            )}
            {["scheduled", "preparing", "sending", "paused"].includes(
              status,
            ) && (
              <Button
                variant="destructive"
                disabled={busy}
                onClick={() => setConfirm("cancel")}
              >
                Cancel campaign
              </Button>
            )}
            {checklist.data && <Preflight data={checklist.data} />}
            <ErrorMessage error={checklist.error} />
            <div className="border-t pt-4">
              <label className="block text-sm">
                Send deliberate [Test] emails
                <Textarea
                  value={testEmails}
                  onChange={(e) => setTestEmails(e.target.value)}
                  placeholder="one@example.com, two@example.com"
                  className="mt-2"
                />
              </label>
              <p className="my-2 text-sm text-muted-foreground">
                Test sends are intentionally repeatable and use the saved
                campaign with a [Test] subject prefix. Each click sends again.
              </p>
              <Button
                variant="info"
                disabled={busy || dirty || !canSend || !testEmails.trim()}
                onClick={() =>
                  action("test-send", {
                    emails: testEmails.split(/[\s,;]+/).filter(Boolean),
                  })
                }
              >
                <Send size={16} /> Send [Test] emails
              </Button>
            </div>
            {editable && (
              <div className="border-t pt-4">
                <Button
                  variant="destructive"
                  disabled={busy}
                  onClick={() => setConfirm("delete")}
                >
                  Delete draft
                </Button>
              </div>
            )}
          </CardContent>
        </Card>
      )}
      <Dialog
        open={confirm !== null}
        onOpenChange={(open) => {
          if (!open && !busy) setConfirm(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {confirm === "start"
                ? "Start campaign?"
                : confirm === "schedule"
                  ? "Schedule campaign?"
                  : confirm === "delete"
                    ? "Delete draft?"
                    : "Cancel campaign?"}
            </DialogTitle>
          </DialogHeader>
          <p className="text-sm">
            {confirm === "delete"
              ? "This permanently deletes this draft and its audience selection."
              : confirm === "cancel"
                ? "Stop sending this campaign. Messages already accepted by SES cannot be recalled."
                : `Send the saved campaign to ${number(checklist.data?.recipients ?? 0)} recipients${confirm === "schedule" ? ` at ${new Date(schedule).toLocaleString()}` : " now"}.`}
          </p>
          {(confirm === "start" || confirm === "schedule") &&
            checklist.data?.checks.warnings?.map((w, i) => (
              <p key={i} className="text-sm">
                Warning: {w.message}
              </p>
            ))}
          <ErrorMessage error={error} />
          <DialogFooter>
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => setConfirm(null)}
            >
              Keep reviewing
            </Button>
            <Button
              variant={
                confirm === "delete" || confirm === "cancel"
                  ? "destructive"
                  : "default"
              }
              disabled={busy}
              onClick={confirmed}
            >
              {busy ? "Working…" : "Confirm"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
function Preflight({ data }: { data: Checklist }) {
  const c = data.checks;
  return (
    <div className="space-y-4 rounded-md border p-4">
      <h3 className="font-medium">
        {c.ready ? "Ready to send" : "Preflight needs attention"}
      </h3>
      <p className="text-sm text-muted-foreground">
        This read-only estimate uses sample merge values. Audience and available
        quota can change before sending.
      </p>
      <dl className="grid grid-cols-2 gap-4 text-sm md:grid-cols-4">
        {[
          ["Recipients", number(data.recipients)],
          ["Message size", bytes(data.message_bytes)],
          ["Upload", bytes(data.upload_bytes)],
          ["Duration", `${Math.ceil(data.duration_seconds / 60)} minutes`],
          ["Suppressed", number(c.suppressed)],
          ["Inactive", number(c.inactive)],
          ["Skipped", number(c.skipped)],
          ["Daily quota remaining", number(c.remaining_daily_quota)],
          ...(c.user_remaining_daily_quota !== undefined
            ? [["Your daily budget", number(c.user_remaining_daily_quota)]]
            : []),
        ].map(([label, value]) => (
          <div key={label}>
            <dt className="text-muted-foreground">{label}</dt>
            <dd className="mt-1 font-medium">{value}</dd>
          </div>
        ))}
      </dl>
      <p className="text-sm">
        HTML: {bytes(c.html_bytes)}.{" "}
        {c.html_clipping
          ? "Above Gmail’s 102 KB clipping threshold."
          : "Below Gmail’s clipping threshold."}{" "}
        Daily quota: {data.fits_daily_quota ? "fits" : "does not fit"}.
      </p>
      {c.errors?.length > 0 && (
        <ul className="list-disc pl-5 text-sm text-destructive">
          {c.errors.map((i, n) => (
            <li key={n}>
              {i.message}
              {i.tag ? ` (${i.tag})` : ""}
            </li>
          ))}
        </ul>
      )}
      {c.warnings?.length > 0 && (
        <ul className="list-disc pl-5 text-sm">
          {c.warnings.map((i, n) => (
            <li key={n}>
              Warning: {i.message}
              {i.tag ? ` (${i.tag})` : ""}
            </li>
          ))}
        </ul>
      )}
      <details>
        <summary className="cursor-pointer text-sm">
          Link checks ({c.links?.length ?? 0})
        </summary>
        <div className="overflow-auto">
          <table className={tableClass}>
            <thead>
              <tr>
                <th>URL</th>
                <th>Status</th>
                <th>Details</th>
              </tr>
            </thead>
            <tbody>
              {c.links?.map((l, i) => (
                <tr key={i}>
                  <td className="break-all">{l.url}</td>
                  <td>
                    {l.status}
                    {l.http_status ? ` (${l.http_status})` : ""}
                  </td>
                  <td>
                    {l.message}
                    {l.personalized ? " Personalized URL" : ""}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </div>
  );
}
