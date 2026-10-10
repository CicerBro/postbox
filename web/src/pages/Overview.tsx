import {
  CalendarDays,
  Send,
  CheckCheck,
  MailOpen,
  MousePointerClick,
  Undo2,
  ShieldAlert,
  Activity,
  Users,
} from "lucide-react";
import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import {
  Card,
  CardHeader,
  CardTitle,
  CardContent,
} from "../components/ui/card";
import { Button } from "../components/ui/button";
import {
  ChartInstructions,
  ChartTooltip,
  useChartInteraction,
} from "../components/chart-tooltip";
interface Overview {
  period: { end: number };
  current: Record<string, number | null>;
  change: Record<string, number | null>;
  growth: {
    day: number;
    active: number;
    subscribed: number;
    unsubscribed: number;
  }[];
  hourly_comparison: { hour: number; today: number; yesterday: number }[];
}
function Chart({
  a,
  b,
  labels,
  name,
  bucketLabels,
  comparisonLabels,
  seriesNames,
}: {
  a: number[];
  b: number[];
  labels: string[];
  name: string;
  bucketLabels: string[];
  comparisonLabels: string[];
  seriesNames: [string, string];
}) {
  const interaction = useChartInteraction(a.length, 30, 690);
  const selected = interaction.index;
  const max = Math.max(...a, ...b, 1);
  const points = (values: number[]) =>
    values
      .map(
        (v, i) =>
          `${30 + (values.length === 1 ? 345 : (i * 690) / Math.max(values.length - 1, 1))},${185 - (v / max) * 155}`,
      )
      .join(" ");
  return (
    <div>
      <div className="relative">
        <ChartInstructions id={interaction.id} />
        <svg
          {...interaction.svgProps}
          role="group"
          aria-label={name}
          viewBox="0 0 740 220"
          className="chart cursor-crosshair rounded focus-visible:outline-2 focus-visible:outline-[var(--ring)]"
        >
          <title>{name}</title>
          {[0, 1, 2, 3].map((i) => (
            <g key={i}>
              <line
                x1="30"
                x2="720"
                y1={185 - (i * 155) / 3}
                y2={185 - (i * 155) / 3}
                className="chart-grid"
              />
              <text x="0" y={189 - (i * 155) / 3}>
                {Math.round((max * i) / 3).toLocaleString()}
              </text>
            </g>
          ))}
          {a.length > 1 && (
            <polygon
              points={`30,185 ${points(a)} 720,185`}
              className="chart-fill"
            />
          )}
          <polyline fill="none" points={points(b)} className="chart-previous" />
          <polyline fill="none" points={points(a)} className="chart-current" />
          {(selected !== null || a.length === 1) && (
            <g pointerEvents="none">
              {selected !== null && (
                <line
                  x1={interaction.x} x2={interaction.x} y1="30" y2="185"
                  stroke="var(--muted-foreground)" strokeDasharray="3 4" opacity=".5"
                />
              )}
              {[a, b].map((values, i) => (
                <circle
                  key={i}
                  cx={selected === null ? 375 : interaction.x}
                  cy={185 - ((values[selected ?? 0] ?? 0) / max) * 155}
                  r="4"
                  fill={`var(--chart-${i + 1})`}
                  stroke="var(--background)"
                  strokeWidth="2"
                />
              ))}
            </g>
          )}
          {labels.map((l, i) => (
            <text
              key={i}
              x={30 + (i * 690) / Math.max(labels.length - 1, 1)}
              y="212"
              textAnchor={
                i === 0 ? "start" : i === labels.length - 1 ? "end" : "middle"
              }
            >
              {l}
            </text>
          ))}
        </svg>
        {selected !== null && (
          <ChartTooltip
          id={interaction.id}
          position={interaction.x / 740 * 100}
          title={bucketLabels[selected] ?? `Bucket ${selected + 1}`}
          rows={[
            { label: seriesNames[0], value: a[selected], color: "var(--chart-1)" },
            {
              label: seriesNames[1],
              value: b[selected] ?? 0,
              color: "var(--chart-2)",
              detail: comparisonLabels[selected],
            },
          ]}
          />
        )}
      </div>
      <details className="chart-data">
        <summary>View chart data</summary>
        <table>
          <thead>
            <tr>
              <th>Bucket</th>
              <th>{seriesNames[0]}</th>
              <th>Comparison bucket</th>
              <th>{seriesNames[1]}</th>
            </tr>
          </thead>
          <tbody>
            {a.map((v, i) => (
              <tr key={i}>
                <td>{bucketLabels[i] ?? i + 1}</td>
                <td>{v}</td>
                <td>{comparisonLabels[i]}</td>
                <td>{b[i] ?? 0}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </details>
    </div>
  );
}
export function OverviewPage({ brandId }: { brandId: number }) {
  const query = useQuery({
    queryKey: ["overview", brandId],
    queryFn: ({ signal }) =>
      api<Overview>("/overview?days=30", { brandId, signal }),
    refetchInterval: 30000,
  });
  if (query.isPending) return <p role="status">Loading overview…</p>;
  if (query.isError)
    return (
      <div role="alert">
        {query.error.message}{" "}
        <Button variant="outline" onClick={() => query.refetch()}>
          Retry
        </Button>
      </div>
    );
  const data = query.data;
  const kpis = [
    ["Sent", "sent", "sent_percent", Send],
    ["Delivered", "delivered_percent", "delivered_points", CheckCheck],
    ["Opened", "opened_percent", "opened_points", MailOpen],
    ["Clicked", "clicked_percent", "clicked_points", MousePointerClick],
    ["Bounced", "bounced_percent", "bounced_points", Undo2],
    ["Complaints", "complaints_percent", "complaints_points", ShieldAlert],
  ] as const;
  const growth = data.growth ?? [];
  const utcDate = (timestamp: number) => new Date(timestamp).toLocaleDateString(
    undefined, { timeZone: "UTC", year: "numeric", month: "short", day: "numeric" },
  );
  const today = new Date(data.period.end);
  const todayStart = Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), today.getUTCDate());
  const hourLabel = (hour: number, previous = false) =>
    `${utcDate(todayStart - (previous ? 86400000 : 0))}, ${String(hour).padStart(2, "0")}:00 UTC`;
  return (
    <>
      <div className="page-heading">
        <div>
          <h1>Overview</h1>
          <p className="page-subtitle">
            Monitor your audience and campaign performance.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <span className="period-label">
            <CalendarDays size={14} />
            Last 30 days
          </span>
          <Button asChild>
            <Link to="/campaigns/new">
              <Send size={14} /> Create campaign
            </Link>
          </Button>
        </div>
      </div>
      <div className="kpi-strip">
        {kpis.map(([label, key, delta, Icon]) => (
          <div className="kpi" key={key}>
            <p>
              {label}
              <Icon aria-hidden="true" />
            </p>
            <strong>
              {data.current[key] == null
                ? "N/A"
                : key === "sent"
                  ? data.current[key]!.toLocaleString()
                  : data.current[key]!.toFixed(1) + "%"}
            </strong>
            <small>
              {data.change[delta] == null ? (
                "No previous-period comparison"
              ) : (
                <>
                  <span
                    className={`kpi-delta ${data.change[delta] === 0 || key === "sent" ? "" : data.change[delta]! > 0 !== (key === "bounced_percent" || key === "complaints_percent") ? "positive" : "negative"}`}
                  >
                    {data.change[delta]! >= 0 ? "+" : ""}
                    {data.change[delta]!.toFixed(1)}
                    {key === "sent" ? "%" : " pp"}
                  </span>{" "}
                  vs previous 30 days
                </>
              )}
            </small>
          </div>
        ))}
      </div>
      <p className="muted text-sm mb-8">
        Campaigns starting in the period. Engagement is attributed to those
        campaigns. Rates use sent emails as denominator.
      </p>
      <div className="chart-panels">
        <Card>
          <CardHeader>
            <div className="section-heading">
              <CardTitle>Subscriber growth</CardTitle>
              <Users size={16} className="muted" />
            </div>
            <p className="muted">
              Active list memberships · daily UTC snapshots
            </p>
            <div className="chart-legend">
              <span>Last 30 days</span>
              <span>Previous 30 days</span>
            </div>
          </CardHeader>
          <CardContent>
            <Chart
              name="Subscriber growth comparing the last 30 days and previous 30 days"
              a={growth.slice(30).map((x) => x.active)}
              b={growth.slice(0, 30).map((x) => x.active)}
              labels={["Day 1", "Day 15", "Day 30"]}
              bucketLabels={growth.slice(30).map((x) => `${utcDate(x.day)} UTC`)}
              comparisonLabels={growth.slice(0, 30).map((x) => `${utcDate(x.day)} UTC`)}
              seriesNames={["Last 30 days", "Previous 30 days"]}
            />
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <div className="section-heading">
              <CardTitle>Sending activity</CardTitle>
              <Activity size={16} className="muted" />
            </div>
            <p className="muted">UTC · today compared with yesterday</p>
            <div className="chart-legend">
              <span>Today</span>
              <span>Yesterday</span>
            </div>
          </CardHeader>
          <CardContent>
            <Chart
              name="Hourly sent emails today and yesterday in UTC"
              a={data.hourly_comparison.map((x) => x.today)}
              b={data.hourly_comparison.map((x) => x.yesterday)}
              labels={["00:00", "06:00", "12:00", "18:00", "23:00"]}
              bucketLabels={data.hourly_comparison.map((x) => hourLabel(x.hour))}
              comparisonLabels={data.hourly_comparison.map((x) => hourLabel(x.hour, true))}
              seriesNames={["Today", "Yesterday"]}
            />
          </CardContent>
        </Card>
      </div>
      {data.current.sent === 0 && (
        <div className="empty-note">
          <span className="flex items-center gap-2">
            <Activity size={15} /> No emails sent in this period. Campaign
            results will appear after sending.
          </span>
        </div>
      )}
    </>
  );
}
