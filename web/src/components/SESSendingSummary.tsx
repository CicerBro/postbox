import { useDateTime } from "../lib/date-time";
import { useQuery } from "@tanstack/react-query";
import { ChevronDown, RefreshCw } from "lucide-react";
import { Link } from "react-router-dom";
import { api } from "../lib/api";
import { sesRegions, type SESSendingStatus } from "../lib/aws-ses";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "./ui/card";

type Connection = {
  has_credentials: boolean;
  updated_at?: number | string;
};

export function SESSendingSummary({ brandId }: { brandId: number }) {
  const { formatDateTime } = useDateTime();
  const connection = useQuery({
    queryKey: ["aws-settings", brandId],
    queryFn: ({ signal }) => api<Connection>("/aws-settings", { brandId, signal }),
    enabled: brandId > 0,
    retry: false,
  });
  const status = useQuery({
    queryKey: ["aws-sending-status", brandId, connection.data?.updated_at],
    queryFn: ({ signal }) => api<SESSendingStatus>("/aws-settings/check", {
      method: "POST", brandId, signal,
    }),
    enabled: connection.isSuccess && connection.data.has_credentials,
    staleTime: 60_000,
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
  const data = connection.isSuccess && connection.data.has_credentials ? status.data : undefined;
  const error = connection.error || status.error;
  const refreshing = connection.isFetching || status.isFetching;
  const region = data && (sesRegions.find((option) => option.value === data.region)?.label || data.region);
  const shortRegion = region?.match(/\(([^()]+)\) \([^()]+\)$/)?.[1] || region;
  const health = data?.enforcement_status;
  const healthLabel = health ? health.charAt(0) + health.slice(1).toLowerCase() : "Unavailable";

  return (
    <Card className="ses-summary" aria-label="Amazon SES sending status" aria-busy={refreshing}>
      <CardHeader>
        <div className="section-heading">
          <CardTitle>Amazon SES</CardTitle>
          {data && <div className="ses-health">
            <Badge variant={health === "HEALTHY" ? "success" : health ? "warning" : "secondary"}>{healthLabel}</Badge>
            {!data.sending_enabled && <Badge variant="destructive">Sending disabled</Badge>}
          </div>}
        </div>
      </CardHeader>
      <CardContent>
        {error && <p role="alert" className="mb-3 text-sm">{error.message}{data ? " Showing the last successful check." : ""}</p>}
        {refreshing && !data && <p role="status" className="muted text-sm">Loading Amazon SES status…</p>}
        {connection.isSuccess && !connection.data.has_credentials && <p className="muted text-sm">
          Add AWS access keys in <Link className="underline" to="/settings">Amazon SES settings</Link> to view sending status and quota.
        </p>}
        {data && <>
          {data.custom_endpoint && <p className="muted text-sm mb-3">A custom AWS endpoint is active. These figures are reported by that endpoint.</p>}
          <dl className="ses-metrics">
            <div><dt className="muted">Sent in last 24 hours</dt><dd className="ses-metric-value">{data.quota.sent_last_24_hours.toLocaleString()}<span className="ses-quota muted">/ {data.quota.max_24_hour_send.toLocaleString()}</span></dd></div>
            <div><dt className="muted">Sends left</dt><dd className="ses-metric-value">{Math.max(0, data.quota.max_24_hour_send - data.quota.sent_last_24_hours).toLocaleString()}</dd></div>
            <div><dt className="muted">Sends per second</dt><dd className="ses-metric-value">{data.quota.max_send_rate.toLocaleString()}</dd></div>
          </dl>
        </>}
      </CardContent>
      <div className="ses-summary-footer">
        {data && <span className="muted">{shortRegion} · {data.production_access_enabled ? "Production" : "Sandbox"}</span>}
        <div className="ses-refresh">
          {data && <span className="muted">Checked {data.checked_at ? <time dateTime={new Date(data.checked_at).toISOString()}>{formatDateTime(data.checked_at)}</time> : "Unavailable"}</span>}
          <Button type="button" variant="ghost" size="icon"
            aria-label="Refresh Amazon SES status from AWS"
            title="Refresh status and quota from AWS"
            disabled={refreshing || (connection.isSuccess && !connection.data.has_credentials)}
            onClick={() => { void (connection.isError ? connection.refetch() : status.refetch()); }}>
            <RefreshCw size={16} aria-hidden="true" className={refreshing ? "animate-spin" : undefined} />
          </Button>
        </div>
      </div>
      {data && <details className="ses-account-details">
        <summary>Account details <ChevronDown size={14} aria-hidden="true" /></summary>
        <div className="ses-account-content">
          <dl>
            <div><dt className="muted">Sending</dt><dd><Badge variant={data.sending_enabled ? "success" : "destructive"}>{data.sending_enabled ? "Enabled" : "Disabled"}</Badge></dd></div>
            <div><dt className="muted">Account access</dt><dd>{data.production_access_enabled ? "Production" : "Sandbox"}</dd></div>
            <div><dt className="muted">SES region</dt><dd>{region}</dd></div>
            <div><dt className="muted">24-hour quota</dt><dd>{data.quota.max_24_hour_send.toLocaleString()}</dd></div>
          </dl>
          <p className="muted">Account quotas are shared by all senders in this AWS account and region.</p>
          <p className="muted">Last checked: {data.checked_at ? formatDateTime(data.checked_at) : "Unavailable"}</p>
        </div>
      </details>}
    </Card>
  );
}
