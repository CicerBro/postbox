import { useDateTime } from "../lib/date-time";
import { onSessionExpired } from "../lib/session-lifecycle";
import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, download } from "../lib/api";
import { SelectField } from "../components/ui/select";
import { SortableTableHead } from "../components/SortableTableHead";
import { useTableSort } from "../lib/use-table-sort";
import { Button } from "../components/ui/button";
import {
  ChartInstructions,
  ChartTooltip,
  useChartInteraction,
} from "../components/chart-tooltip";
import {
  Download,
  Users,
  Mail,
  UserMinus,
  ShieldAlert,
  Monitor,
  Smartphone,
  CheckCircle2,
  Eye,
  MousePointerClick,
  CircleAlert,
  ArrowLeft,
  Activity,
  Copy,
} from "lucide-react";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "../components/ui/card";
import {
  StatusBadge,
  isFinishedCampaign,
  Campaign,
  ErrorMessage,
  number,
  tableClass,
} from "./campaign-shared";

type Stats = Record<string, number>;
const countryNames = new Intl.DisplayNames(["en"], { type: "region", fallback: "none" });
function countryName(country: string) {
  const code = country.trim().toUpperCase();
  if (!code || code === "ZZ") return "Unknown";
  return /^[A-Z]{2}$/.test(code) ? countryNames.of(code) ?? country : country;
}
type LinkStats = {
  id: number;
  url: string;
  clicks_total: number;
  clicks_unique: number;
  clicks_human_total: number;
  clicks_human_unique: number;
};
type Hour = { hour: number; opens: number; clicks: number; deliveries: number };
type Report = {
  can_reactivate?: boolean;
  stats: Stats[];
  links: LinkStats[];
  countries: { country: string; opens: number; clicks: number }[];
  hourly: Hour[];
  recent?: Record<"opened" | "unsubscribed" | "bounced" | "complained", RecentRecipient[]>;
  delivery?: { first_sent_at: number | null; last_sent_at: number | null; from_name: string; from_email: string; sender_snapshotted: boolean; historical?: boolean; delivered_estimated?: boolean };
  targets?: { target_type: "list" | "segment"; target_id: number; mode: "include" | "exclude"; name: string | null }[];
};
type RecentRecipient = { id: number; email: string; name: string | null; at: number };
type Recipient = {
  id: number;
  email: string;
  status: string;
  error: string | null;
  message_id: string | null;
  sent_at: number | null;
};
export function CampaignReportPage({ brandId }: { brandId: number }) {
  const { id } = useParams();
  return (
    <ReportView key={`${brandId}:${id}`} brandId={brandId} id={Number(id)} />
  );
}
function ReportView({ brandId, id }: { brandId: number; id: number }) {
  const { formatDateTime } = useDateTime();
  const navigate = useNavigate();
  const countrySort = useTableSort<"country" | "opens" | "clicks">("opens", "desc");
  const client = useQueryClient(),
    [status, setStatus] = useState(""),
    [after, setAfter] = useState(0),
    [history, setHistory] = useState<number[]>([]),
    [stream, setStream] = useState("Connecting live updates"),
    [exportError, setExportError] = useState<unknown>(null),
    [exporting, setExporting] = useState(false),
    [duplicating, setDuplicating] = useState(false),
    [duplicateError, setDuplicateError] = useState<unknown>(null),
    [reactivating, setReactivating] = useState(false),
    [reactivateError, setReactivateError] = useState<unknown>(null),
    [previewWidth, setPreviewWidth] = useState<"desktop" | "mobile">("desktop");
  const valid = Number.isSafeInteger(id) && id > 0 && brandId > 0;
  const campaign = useQuery({
    queryKey: ["campaign", brandId, id],
    queryFn: () => api<Campaign[]>(`/campaigns/${id}`, { brandId }),
    enabled: valid,
    refetchInterval: 10000,
  });
  const report = useQuery({
    queryKey: ["campaign-report", brandId, id],
    queryFn: () => api<Report>(`/campaigns/${id}/report`, { brandId }),
    enabled: valid,
    refetchInterval: 15000,
  });
  const preview = useQuery({
    queryKey: ["campaign-preview", brandId, id],
    queryFn: () => api<{ subject: string; html: string; text: string }>(`/campaigns/${id}/preview`, { brandId }),
    enabled: valid && Boolean(campaign.data?.length),
  });
  const previewHTML = useMemo(() => inertPreview(preview.data?.html ?? ""), [preview.data?.html]);
  const recipients = useQuery({
    queryKey: ["campaign-recipients", brandId, id, status, after],
    queryFn: () =>
      api<Recipient[]>(
        `/campaigns/${id}/recipients?after=${after}&limit=50${status ? `&status=${status}` : ""}`,
        { brandId },
      ),
    enabled: valid,
    refetchInterval: 10000,
  });
  useEffect(() => {
    if (!valid) return;
    const source = new EventSource(
      `/api/v1/campaigns/${id}/events?brand_id=${brandId}`,
    );
    const listener = (event: MessageEvent<string>) => {
      try {
        const stats: unknown = JSON.parse(event.data);
        if (!Array.isArray(stats)) return;
        client.setQueryData<Report>(
          ["campaign-report", brandId, id],
          (current) =>
            current ? { ...current, stats: stats as Stats[] } : current,
        );
        setStream("Live updates connected");
      } catch {
        setStream("Invalid live update. Reports refresh automatically.");
      }
    };
    source.addEventListener("stats", listener);
    source.onopen = () => setStream("Live updates connected");
    source.onerror = () => {
      source.close();
      setStream("Live updates disconnected. Reports refresh automatically.");
      // EventSource hides HTTP status. An authenticated request identifies expiry.
      void api(`/campaigns/${id}/report`, { brandId }).catch(() => {});
    };
    const stop = onSessionExpired(() => source.close());
    return () => { stop(); source.close(); };
  }, [brandId, id, client, valid]);
  async function exportCSV() {
    setExporting(true);
    setExportError(null);
    try {
      await download(
        `/campaigns/${id}/export`,
        brandId,
        `campaign-${id}-recipients.csv`,
      );
    } catch (e) {
      setExportError(e);
    } finally {
      setExporting(false);
    }
  }
  async function duplicateCampaign() {
    setDuplicating(true);
    setDuplicateError(null);
    try {
      const copy = await api<{ id: number }>(`/campaigns/${id}/duplicate`, {
        method: "POST",
        brandId,
      });
      client.invalidateQueries({ queryKey: ["campaigns", brandId] });
      navigate(`/campaigns/${copy.id}`);
    } catch (e) {
      setDuplicateError(e);
    } finally {
      setDuplicating(false);
    }
  }
  async function reactivateCampaign() {
    setReactivating(true);
    setReactivateError(null);
    try {
      await api(`/campaigns/${id}/reactivate`, { method: "POST", brandId });
      await Promise.all([
        client.invalidateQueries({ queryKey: ["campaign", brandId, id] }),
        client.invalidateQueries({ queryKey: ["campaigns", brandId] }),
        client.invalidateQueries({ queryKey: ["campaign-report", brandId, id] }),
        client.invalidateQueries({ queryKey: ["campaign-preview", brandId, id] }),
        client.invalidateQueries({ queryKey: ["campaign-recipients", brandId, id] }),
      ]);
      navigate(`/campaigns/${id}`);
    } catch (e) {
      setReactivateError(e);
      void report.refetch();
    } finally {
      setReactivating(false);
    }
  }
  if (!valid) return <ErrorMessage error={new Error("Invalid campaign ID")} />;
  const stats = report.data?.stats?.[0];
  const metrics: [string, string][] = [
    ["recipients", "Recipients"],
    ["sent", "Sent"],
    ["delivered", "Delivered"],
    ["failed", "Failed"],
    ["unknown", "Unknown"],
    ["skipped", "Skipped"],
    ["opens_unique", "Unique opens"],
    ["opens_total", "Total opens"],
    ["opens_human_unique", "Unique human opens"],
    ["opens_human_total", "Total human opens"],
    ["clicks_unique", "Unique clicks"],
    ["clicks_total", "Total clicks"],
    ["clicks_human_unique", "Unique human clicks"],
    ["clicks_human_total", "Total human clicks"],
    ["bounces_hard", "Hard bounces"],
    ["bounces_soft", "Soft bounces"],
    ["complaints", "Complaints"],
    ["unsubscribes", "Unsubscribes"],
  ];
  const sent = stats?.sent ?? 0;
  const bounces = stats && stats.bounces_hard !== undefined && stats.bounces_soft !== undefined
    ? stats.bounces_hard + stats.bounces_soft : undefined;
  const highlights = [
    { label: "Unique opens", count: stats?.opens_unique, denominator: sent, basis: "of sent", icon: Eye },
    { label: "Unique clicks", count: stats?.clicks_unique, denominator: sent, basis: "of sent", icon: MousePointerClick },
    { label: "Click-to-open rate", count: stats?.clicks_unique, denominator: stats?.opens_unique ?? 0, basis: "of unique opens", icon: Activity },
    { label: "Unsubscribed", count: stats?.unsubscribes, denominator: sent, basis: "of sent", icon: UserMinus },
    { label: "Bounced", count: bounces, denominator: sent, basis: "of sent", icon: CircleAlert },
    { label: "Spam complaints", count: stats?.complaints, denominator: sent, basis: "of sent", icon: ShieldAlert },
  ];
  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <Link
            className="inline-flex items-center gap-1 text-sm text-muted-foreground"
            to="/campaigns"
          >
            <ArrowLeft size={14} /> All campaigns
          </Link>
          <p className="mt-3 text-sm font-medium text-muted-foreground">Campaign report</p>
          <h1 className="mt-1 text-2xl font-bold tracking-tight">
            {campaign.data?.[0]?.subject ?? "Campaign report"}
          </h1>
          {campaign.data?.[0] && (
            <StatusBadge status={campaign.data[0].status} />
          )}
          <p
            role="status"
            className="mt-2 flex items-center gap-2 text-sm text-muted-foreground"
          >
            <Activity size={14} aria-hidden="true" />
            {stream}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
        {campaign.data?.[0]?.status === "cancelled" && report.data?.can_reactivate && <Button disabled={reactivating} onClick={reactivateCampaign}>{reactivating ? "Returning to draft…" : "Return to draft"}</Button>}
        {campaign.data?.[0] && !isFinishedCampaign(campaign.data[0].status) && <Button variant="outline" asChild><Link to={`/campaigns/${id}`}>Manage campaign</Link></Button>}
        {campaign.data?.[0] && <Button variant="outline" disabled={duplicating} onClick={duplicateCampaign}><Copy size={16} /> {duplicating ? "Duplicating…" : "Duplicate"}</Button>}
        <Button variant="outline" disabled={exporting} onClick={exportCSV}>
          <Download size={16} />{" "}
          {exporting ? "Exporting…" : "Export recipients CSV"}
        </Button>
        </div>
      </div>
      <ErrorMessage error={campaign.error ?? report.error ?? exportError ?? duplicateError ?? reactivateError} />
      {report.data?.delivery && (
        <Card><CardContent className="pt-5">
          <dl className="grid gap-5 sm:grid-cols-2 xl:grid-cols-4">
            <div><dt className="row-subtitle">Sent</dt><dd className="mt-1 text-sm font-medium">{report.data.delivery.first_sent_at ? formatDateTime(report.data.delivery.first_sent_at) : "No send recorded"}</dd></div>
            <div><dt className="row-subtitle">{report.data.delivery.sender_snapshotted ? "From" : "Current brand sender"}</dt><dd className="mt-1 break-words text-sm font-medium">{report.data.delivery.from_name}<span className="block text-muted-foreground">{report.data.delivery.from_email}</span></dd></div>
            <div><dt className="row-subtitle">Audience</dt><dd className="mt-1 text-sm font-medium">{report.data.targets?.length ? report.data.targets.map(target => `${target.mode === "exclude" ? "Exclude: " : ""}${target.name ?? `${target.target_type} #${target.target_id}`}`).join(", ") : "No audience targets"}</dd></div>
            <div><dt className="row-subtitle">Recipients</dt><dd className="mt-1 text-sm font-medium">{stats?.recipients === undefined ? "Unavailable" : number(stats.recipients)}<span className="block text-muted-foreground">{number(sent)} sent · {number(stats?.delivered ?? 0)} {report.data.delivery.delivered_estimated ? "estimated delivered" : "delivered"}</span></dd></div>
          </dl>
        </CardContent></Card>
      )}
      {report.isPending ? (
        <p role="status">Loading report…</p>
      ) : !stats ? (
        <p className="rounded-md border p-6 text-sm text-muted-foreground">
          No report counters yet. Counters appear once the campaign audience is
          prepared.
        </p>
      ) : (
        <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
          {highlights.map(({ label, count, denominator, basis, icon: Icon }) => (
            <Card key={label} className="metric-card">
              <CardContent className="p-4">
                <div className="flex items-start justify-between gap-2">
                  <p className="text-xs font-medium text-muted-foreground">{label}</p>
                  <span className="metric-icon"><Icon size={16} /></span>
                </div>
                <p className="mt-3 text-2xl font-semibold tracking-tight">{count === undefined ? "Unavailable" : denominator > 0 ? `${((count / denominator) * 100).toFixed(2)}%` : "Unavailable"}</p>
                <p className="mt-1 text-xs text-muted-foreground">{count === undefined ? "No counter recorded" : `${number(count)} recorded`}</p>
                <p className="mt-1 text-xs text-muted-foreground">{denominator > 0 ? basis : "No denominator available"}</p>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
      {report.data?.delivery?.historical && <p className="text-xs text-muted-foreground">Imported Sendy report. Delivery is estimated as sent minus hard bounces when no delivery counter was supplied. Unsubscribes, bounces and complaints reflect the archived subscriber statuses attributed to this campaign; later Sendy campaigns may have reassigned older statuses. Recipient delivery events and human engagement classifications are unavailable.</p>}
      <p className="text-xs text-muted-foreground">Open and click rates include all recorded activity. Human engagement counters are available below.</p>
      <div className="grid items-start gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
        <Card className="min-w-0">
          <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
            <CardTitle className="flex items-center gap-2"><Mail size={18} /> Newsletter preview</CardTitle>
            <div className="flex gap-1" role="group" aria-label="Preview width">
              <Button size="sm" variant={previewWidth === "desktop" ? "outline" : "ghost"} aria-pressed={previewWidth === "desktop"} onClick={() => setPreviewWidth("desktop")}><Monitor size={15} /> Desktop</Button>
              <Button size="sm" variant={previewWidth === "mobile" ? "outline" : "ghost"} aria-pressed={previewWidth === "mobile"} onClick={() => setPreviewWidth("mobile")}><Smartphone size={15} /> Mobile</Button>
            </div>
          </CardHeader>
          <CardContent>
            <ErrorMessage error={preview.error} />
            {preview.isPending ? <p role="status">Loading newsletter preview…</p> : preview.data && <div className="overflow-hidden rounded-md border bg-muted p-2"><iframe title="Sent newsletter preview" sandbox="" referrerPolicy="no-referrer" srcDoc={previewHTML} className="mx-auto block h-[460px] rounded bg-white" style={{ width: previewWidth === "mobile" ? "var(--newsletter-mobile-width)" : "100%", maxWidth: "100%" }} /></div>}
            <p className="mt-3 text-xs text-muted-foreground">Saved newsletter with sample personalization. Scripts and navigation are disabled in the preview.</p>
          </CardContent>
        </Card>
        <Card className="min-w-0">
          <CardHeader><CardTitle>Activity over time</CardTitle><p className="row-subtitle">Deliveries, opens, and clicks across the campaign timeline.</p></CardHeader>
          <CardContent><ActivityChart rows={report.data?.hourly ?? []} /></CardContent>
        </Card>
      </div>
      <div className="grid items-start gap-6 md:grid-cols-2">
        <RecentActivity title="Recently opened" description="Last 10 recipients by their first recorded open, including automated activity." rows={report.data?.recent?.opened ?? []} loading={report.isPending} />
        <RecentActivity title="Recent unsubscribes" description="Last 10 recipients who unsubscribed from this campaign." rows={report.data?.recent?.unsubscribed ?? []} loading={report.isPending} />
        <RecentActivity title="Recent bounces" description="Last 10 recipients with a recorded bounce." rows={report.data?.recent?.bounced ?? []} loading={report.isPending} />
        <RecentActivity title="Recent spam complaints" description="Last 10 recipients who reported this campaign as spam." rows={report.data?.recent?.complained ?? []} loading={report.isPending} />
      </div>
      {stats && (
        <Card>
          <CardContent className="pt-5">
            <details>
              <summary className="cursor-pointer text-sm font-medium">
                View all engagement and delivery counters
              </summary>
              <dl className="mt-5 grid grid-cols-2 gap-x-6 gap-y-5 md:grid-cols-4 xl:grid-cols-6">
                {metrics.map(([key, label]) => (
                  <div key={key}>
                    <dt className="row-subtitle">{label}{key === "delivered" && report.data?.delivery?.delivered_estimated ? " (estimated)" : ""}</dt>
                    <dd className="mt-1 text-lg font-semibold">
                      {stats[key] === undefined
                        ? "Unavailable"
                        : number(stats[key])}
                    </dd>
                    {[
                      "delivered",
                      "opens_human_unique",
                      "clicks_human_unique",
                      "complaints",
                      "unsubscribes",
                    ].includes(key) &&
                      stats[key] !== undefined && (
                        <p className="mt-1 text-xs text-muted-foreground">
                          {stats.sent > 0
                            ? `${((stats[key] / stats.sent) * 100).toFixed(2)}% of sent`
                            : "No sent denominator"}
                        </p>
                      )}
                  </div>
                ))}
              </dl>
            </details>
          </CardContent>
        </Card>
      )}
      <Card>
        <CardHeader>
          <CardTitle>Link clicks</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="overflow-auto">
            <table className={tableClass}>
              <thead>
                <tr>
                  <th>URL</th>
                  <th>Total</th>
                  <th>Unique</th>
                  <th>Human total</th>
                  <th>Human unique</th>
                </tr>
              </thead>
              <tbody>
                {report.data?.links?.map((l) => (
                  <tr key={l.id}>
                    <td className="max-w-lg break-all">{l.url}</td>
                    <td>{number(l.clicks_total)}</td>
                    <td>{number(l.clicks_unique)}</td>
                    <td>{number(l.clicks_human_total)}</td>
                    <td>{number(l.clicks_human_unique)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            {!report.data?.links?.length && (
              <p className="py-4 text-sm text-muted-foreground">
                No tracked links yet.
              </p>
            )}
          </div>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Countries</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="overflow-auto">
            <table className={tableClass}>
              <thead>
                <tr>
                  <SortableTableHead label="Country" {...countrySort.header("country")} />
                  <SortableTableHead label="Opens" {...countrySort.header("opens")} />
                  <SortableTableHead label="Clicks" {...countrySort.header("clicks")} />
                </tr>
              </thead>
              <tbody>
                {countrySort.sort(report.data?.countries ?? [], (c, key) => key === "country" ? countryName(c.country) : c[key]).map((c) => (
                  <tr key={c.country}>
                    <td>{countryName(c.country)}</td>
                    <td>{number(c.opens)}</td>
                    <td>{number(c.clicks)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            {!report.data?.countries?.length && (
              <p className="py-4 text-sm text-muted-foreground">
                No country activity recorded.
              </p>
            )}
          </div>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Recipient outcomes</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <p className="text-sm text-muted-foreground">
            Unknown outcomes are terminal. SES may already have accepted those
            messages, so uncertain recipients cannot be resent.
          </p>
          <label className="block max-w-xs text-sm">
            Delivery status
            <SelectField
              aria-label="Delivery status"
              className="mt-2"
              value={status}
              onValueChange={(value) => {
                setStatus(value);
                setAfter(0);
                setHistory([]);
              }}
              options={[
                { value: "", label: "All outcomes" },
                ...[
                  "pending",
                  "claimed",
                  "sent",
                  "failed",
                  "unknown",
                  "skipped",
                ].map((value) => ({ value, label: value })),
              ]}
            />
          </label>
          <ErrorMessage error={recipients.error} />
          {recipients.isPending ? (
            <p role="status">Loading recipient outcomes…</p>
          ) : (
            <div className="overflow-auto">
              <table className={tableClass}>
                <thead>
                  <tr>
                    <th>Email</th>
                    <th>Status</th>
                    <th>Message ID</th>
                    <th>Sent</th>
                    <th>Error</th>
                  </tr>
                </thead>
                <tbody>
                  {recipients.data?.map((r) => (
                    <tr key={r.id}>
                      <td className="row-title">{r.email}</td>
                      <td>
                        <StatusBadge status={r.status} />
                      </td>
                      <td className="break-all">{r.message_id ?? "None"}</td>
                      <td>
                        {r.sent_at
                          ? formatDateTime(r.sent_at)
                          : "Not recorded"}
                      </td>
                      <td className="max-w-md break-words">{r.error ?? ""}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {!recipients.data?.length && (
                <p className="py-4 text-sm text-muted-foreground">
                  No recipients match this page and status.
                </p>
              )}
            </div>
          )}
          <div className="table-footer flex gap-2">
            <span className="row-subtitle mr-auto">
              Page {history.length + 1}
            </span>
            <Button
              variant="outline"
              disabled={!history.length}
              onClick={() => {
                setAfter(history.at(-1) ?? 0);
                setHistory(history.slice(0, -1));
              }}
            >
              Previous
            </Button>
            <Button
              variant="outline"
              disabled={recipients.data?.length !== 50}
              onClick={() => {
                setHistory([...history, after]);
                setAfter(recipients.data!.at(-1)!.id);
              }}
            >
              Next
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
function inertPreview(html: string) {
  const document = new DOMParser().parseFromString(html, "text/html");
  document.querySelectorAll("a, area").forEach(element => {
    element.removeAttribute("href");
    element.removeAttribute("target");
    element.removeAttribute("ping");
  });
  document.querySelectorAll("script, iframe, object, embed, base, meta[http-equiv]").forEach(element => element.remove());
  const policy = document.createElement("meta");
  policy.httpEquiv = "Content-Security-Policy";
  policy.content = "default-src 'none'; img-src https: data:; style-src 'unsafe-inline' https:; font-src https: data:; form-action 'none'";
  document.head.prepend(policy);
  return `<!doctype html>${document.documentElement.outerHTML}`;
}
function RecentActivity({ title, description, rows, loading }: { title: string; description: string; rows: RecentRecipient[]; loading: boolean }) {
  const { formatDateTime } = useDateTime();
  return <Card className="min-w-0"><CardHeader><CardTitle>{title}</CardTitle><p className="row-subtitle">{description}</p></CardHeader><CardContent>
    {loading ? <p role="status" className="text-sm text-muted-foreground">Loading activity…</p> : rows.length ? <div className="overflow-auto"><table className={tableClass}><thead><tr><th>Recipient</th><th>When</th></tr></thead><tbody>{rows.map(row => <tr key={row.id}><td className="break-all"><span className="row-title">{row.email || "Email unavailable"}</span>{row.name && <span className="row-subtitle block">{row.name}</span>}</td><td className="whitespace-nowrap text-muted-foreground">{formatDateTime(row.at)}</td></tr>)}</tbody></table></div> : <p className="py-4 text-sm text-muted-foreground">No activity recorded yet.</p>}
  </CardContent></Card>;
}
function ActivityChart({ rows }: { rows: Hour[] }) {
  const { timeZone, formatDateTime } = useDateTime();
  const interaction = useChartInteraction(rows.length, 50, 750);
  const selected = interaction.index;
  if (!rows.length)
    return (
      <p className="text-sm text-muted-foreground">No hourly engagement yet.</p>
    );
  const max = Math.max(
    1,
    ...rows.flatMap((r) => [r.opens, r.clicks, r.deliveries]),
  );
  const points = (key: "opens" | "clicks" | "deliveries") =>
    rows
      .map(
        (r, i) =>
          `${50 + (rows.length === 1 ? 375 : (i / (rows.length - 1)) * 750)},${230 - (r[key] / max) * 200}`,
      )
      .join(" ");
  return (
    <div className="space-y-3">
      <div className="overflow-x-auto">
        <div className="relative min-w-[500px]">
          <ChartInstructions id={interaction.id} />
          <svg
            {...interaction.svgProps}
            viewBox="0 0 850 280"
            className="w-full cursor-crosshair rounded text-foreground focus-visible:outline-2 focus-visible:outline-[var(--ring)]"
            role="group"
            aria-label="Hourly opens, clicks and deliveries. Full data is available below."
          >
            {[30, 80, 130, 180].map((y) => (
              <line
                key={y}
                x1="50"
                y1={y}
                x2="800"
                y2={y}
                stroke="currentColor"
                opacity=".06"
              />
            ))}
            <line
              x1="50"
              y1="230"
              x2="800"
              y2="230"
              stroke="currentColor"
              opacity=".2"
            />
            <text x="0" y="35" fill="currentColor" fontSize="12">
              {number(max)}
            </text>
            <text x="25" y="235" fill="currentColor" fontSize="12">
              0
            </text>
            {(["deliveries", "opens", "clicks"] as const).map((key, i) => (
              <g key={key}>
                <polyline
                  points={points(key)}
                  fill="none"
                  stroke={
                    ["var(--chart-1)", "var(--chart-2)", "var(--chart-3)"][i]
                  }
                  strokeWidth="2.5"
                  strokeDasharray={i === 1 ? "6 3" : i === 2 ? "2 3" : undefined}
                  opacity="1"
                />
                {rows.length === 1 && (
                  <circle
                    cx="425"
                    cy={230 - (rows[0][key] / max) * 200}
                    r="3"
                    fill={
                      ["var(--chart-1)", "var(--chart-2)", "var(--chart-3)"][i]
                    }
                  />
                )}
              </g>
            ))}
            {selected !== null && (
              <g pointerEvents="none">
                <line
                  x1={interaction.x} x2={interaction.x} y1="30" y2="230"
                  stroke="var(--muted-foreground)" strokeDasharray="3 4" opacity=".5"
                />
                {(["deliveries", "opens", "clicks"] as const).map((key, i) => (
                  <circle
                    key={key}
                    cx={interaction.x}
                    cy={230 - (rows[selected][key] / max) * 200}
                    r="4"
                    fill={`var(--chart-${i + 1})`}
                    stroke="var(--background)"
                    strokeWidth="2"
                  />
                ))}
              </g>
            )}
            <text x="50" y="265" fill="currentColor" fontSize="12">
              {formatDateTime(rows[0].hour)}
            </text>
            <text
              x="800"
              y="265"
              textAnchor="end"
              fill="currentColor"
              fontSize="12"
            >
              {formatDateTime(rows.at(-1)!.hour)}
            </text>
          </svg>
          {selected !== null && (
            <ChartTooltip
              id={interaction.id}
              position={interaction.x / 850 * 100}
              title={formatDateTime(rows[selected].hour, {
                year: "numeric", month: "short", day: "numeric",
                hour: "2-digit", minute: "2-digit", timeZoneName: "short",
              })}
              rows={(["deliveries", "opens", "clicks"] as const).map((key, i) => ({
                label: ["Deliveries", "Opens", "Clicks"][i],
                value: rows[selected][key],
                color: `var(--chart-${i + 1})`,
              }))}
            />
          )}
        </div>
      </div>
      <div className="activity-legend flex flex-wrap items-center gap-5 text-xs text-muted-foreground">
        {(["Deliveries", "Opens", "Clicks"] as const).map((label, i) => (
          <span key={label} className="flex items-center gap-2">
            <span
              className="h-2 w-2 rounded-full"
              style={{
                background: [
                  "var(--chart-1)",
                  "var(--chart-2)",
                  "var(--chart-3)",
                ][i],
              }}
            />
            {label}
          </span>
        ))}
        <span className="ml-auto">Times shown in {timeZone}.</span>
      </div>
      <details>
        <summary className="cursor-pointer text-sm">View hourly data</summary>
        <div className="max-h-72 overflow-auto">
          <table className={tableClass}>
            <thead>
              <tr>
                <th>Hour</th>
                <th>Deliveries</th>
                <th>Opens</th>
                <th>Clicks</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.hour}>
                  <td>{formatDateTime(r.hour)}</td>
                  <td>{number(r.deliveries)}</td>
                  <td>{number(r.opens)}</td>
                  <td>{number(r.clicks)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </div>
  );
}
