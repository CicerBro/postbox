import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, download } from "../lib/api";
import { Button } from "../components/ui/button";
import { Badge } from "../components/ui/badge";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "../components/ui/card";
import {
  Campaign,
  ErrorMessage,
  number,
  selectClass,
  tableClass,
} from "./campaign-shared";

type Stats = Record<string, number>;
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
  stats: Stats[];
  links: LinkStats[];
  countries: { country: string; opens: number; clicks: number }[];
  hourly: Hour[];
};
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
  const client = useQueryClient(),
    [status, setStatus] = useState(""),
    [after, setAfter] = useState(0),
    [history, setHistory] = useState<number[]>([]),
    [stream, setStream] = useState("Connecting live updates"),
    [exportError, setExportError] = useState<unknown>(null),
    [exporting, setExporting] = useState(false);
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
    source.onerror = () =>
      setStream("Live updates reconnecting. Reports refresh automatically.");
    return () => source.close();
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
  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <Link className="text-sm underline" to={`/campaigns/${id}`}>
            Campaign
          </Link>
          <h1 className="mt-2 text-2xl font-semibold">
            {campaign.data?.[0]?.subject ?? "Campaign report"}
          </h1>
          {campaign.data?.[0] && (
            <Badge variant="outline">{campaign.data[0].status}</Badge>
          )}
          <p role="status" className="mt-2 text-sm text-muted-foreground">
            {stream}
          </p>
        </div>
        <Button variant="outline" disabled={exporting} onClick={exportCSV}>
          {exporting ? "Exporting…" : "Export recipients CSV"}
        </Button>
      </div>
      <ErrorMessage error={campaign.error ?? report.error ?? exportError} />
      {report.isPending ? (
        <p role="status">Loading report…</p>
      ) : !stats ? (
        <p className="rounded-md border p-6 text-sm text-muted-foreground">
          No report counters yet. Counters appear once the campaign audience is
          prepared.
        </p>
      ) : (
        <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
          {metrics.map(([key, label]) => (
            <Card key={key}>
              <CardContent className="p-4">
                <p className="text-xs text-muted-foreground">{label}</p>
                <p className="mt-2 text-xl font-semibold">
                  {stats[key] === undefined
                    ? "Unavailable"
                    : number(stats[key])}
                </p>
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
              </CardContent>
            </Card>
          ))}
        </div>
      )}
      <Card>
        <CardHeader>
          <CardTitle>Activity over time</CardTitle>
        </CardHeader>
        <CardContent>
          <ActivityChart rows={report.data?.hourly ?? []} />
        </CardContent>
      </Card>
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
                  <th>Country</th>
                  <th>Opens</th>
                  <th>Clicks</th>
                </tr>
              </thead>
              <tbody>
                {report.data?.countries?.map((c) => (
                  <tr key={c.country}>
                    <td>{c.country || "Unknown"}</td>
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
            <select
              className={selectClass}
              value={status}
              onChange={(e) => {
                setStatus(e.target.value);
                setAfter(0);
                setHistory([]);
              }}
            >
              <option value="">All outcomes</option>
              {[
                "pending",
                "claimed",
                "sent",
                "failed",
                "unknown",
                "skipped",
              ].map((s) => (
                <option key={s}>{s}</option>
              ))}
            </select>
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
                      <td>{r.email}</td>
                      <td>
                        <Badge variant="outline">{r.status}</Badge>
                      </td>
                      <td className="break-all">{r.message_id ?? "None"}</td>
                      <td>
                        {r.sent_at
                          ? new Date(r.sent_at).toLocaleString()
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
          <div className="flex gap-2">
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
function ActivityChart({ rows }: { rows: Hour[] }) {
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
        <svg
          viewBox="0 0 850 280"
          className="min-w-[500px] w-full text-foreground"
          role="img"
          aria-label="Hourly opens, clicks and deliveries. Full data is available below."
        >
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
                stroke="currentColor"
                strokeWidth="2"
                strokeDasharray={i === 1 ? "6 3" : i === 2 ? "2 3" : undefined}
                opacity={1 - i * 0.2}
              />
              {rows.length === 1 && (
                <circle
                  cx="425"
                  cy={230 - (rows[0][key] / max) * 200}
                  r="3"
                  fill="currentColor"
                />
              )}
            </g>
          ))}
          <text x="50" y="265" fill="currentColor" fontSize="12">
            {new Date(rows[0].hour).toLocaleString()}
          </text>
          <text
            x="800"
            y="265"
            textAnchor="end"
            fill="currentColor"
            fontSize="12"
          >
            {new Date(rows.at(-1)!.hour).toLocaleString()}
          </text>
        </svg>
      </div>
      <p className="text-xs text-muted-foreground">
        Solid: deliveries · Dashed: opens · Dotted: clicks. Times use your
        browser timezone.
      </p>
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
                  <td>{new Date(r.hour).toLocaleString()}</td>
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
