import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import {
  Card,
  CardHeader,
  CardTitle,
  CardContent,
} from "../components/ui/card";
import { Button } from "../components/ui/button";
interface Overview {
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
}: {
  a: number[];
  b: number[];
  labels: string[];
  name: string;
}) {
  const max = Math.max(...a, ...b, 1);
  const points = (values: number[]) =>
    values
      .map(
        (v, i) =>
          `${30 + (i * 690) / Math.max(values.length - 1, 1)},${185 - (v / max) * 155}`,
      )
      .join(" ");
  return (
    <div>
      <svg role="img" aria-label={name} viewBox="0 0 740 220" className="chart">
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
        <polyline fill="none" points={points(b)} className="chart-previous" />
        <polyline fill="none" points={points(a)} className="chart-current" />
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
      <details className="chart-data">
        <summary>View chart data</summary>
        <table>
          <thead>
            <tr>
              <th>Bucket</th>
              <th>Current</th>
              <th>Previous</th>
            </tr>
          </thead>
          <tbody>
            {a.map((v, i) => (
              <tr key={i}>
                <td>{i + 1}</td>
                <td>{v}</td>
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
    ["Sent", "sent", "sent_percent"],
    ["Delivered", "delivered_percent", "delivered_points"],
    ["Opened", "opened_percent", "opened_points"],
    ["Clicked", "clicked_percent", "clicked_points"],
    ["Bounced", "bounced_percent", "bounced_points"],
    ["Complaints", "complaints_percent", "complaints_points"],
  ];
  const growth = data.growth ?? [];
  return (
    <>
      <div className="page-heading">
        <div>
          <p className="eyebrow">Your newsletter at a glance</p>
          <h1>Overview</h1>
        </div>
        <span className="period-label">Last 30 days</span>
      </div>
      <div className="kpi-strip">
        {kpis.map(([label, key, delta]) => (
          <div className="kpi" key={key}>
            <p>
              {label}
              {key !== "sent" ? " %" : ""}
            </p>
            <strong>
              {data.current[key] == null
                ? "N/A"
                : key === "sent"
                  ? data.current[key]!.toLocaleString()
                  : data.current[key]!.toFixed(1) + "%"}
            </strong>
            <small>
              {data.change[delta] == null
                ? "No previous-period comparison"
                : `${data.change[delta]! >= 0 ? "+" : ""}${data.change[delta]!.toFixed(1)}${key === "sent" ? "%" : " pp"} vs previous 30 days`}
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
            <CardTitle>Subscriber growth</CardTitle>
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
            />
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Emails sent per hour</CardTitle>
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
            />
          </CardContent>
        </Card>
      </div>
      {data.current.sent === 0 && (
        <div className="empty-note">
          No emails sent in this period. Your campaign results will appear here
          after sending.
        </div>
      )}
    </>
  );
}
