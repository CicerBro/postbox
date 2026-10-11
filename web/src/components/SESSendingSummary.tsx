import { useQuery } from "@tanstack/react-query";
import { RefreshCw } from "lucide-react";
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
  const health = data?.enforcement_status;
  const healthLabel = health ? health.charAt(0) + health.slice(1).toLowerCase() : "Unavailable";

  return (
    <Card className="mb-8" aria-label="Amazon SES sending status" aria-busy={refreshing}>
      <CardHeader>
        <div className="section-heading">
          <CardTitle>Amazon SES</CardTitle>
          <Button type="button" variant="ghost" size="icon"
            aria-label="Refresh Amazon SES status from AWS"
            title="Refresh status and quota from AWS"
            disabled={refreshing || (connection.isSuccess && !connection.data.has_credentials)}
            onClick={() => { void (connection.isError ? connection.refetch() : status.refetch()); }}>
            <RefreshCw size={16} aria-hidden="true" className={refreshing ? "animate-spin" : undefined} />
          </Button>
        </div>
        <p className="muted text-sm">Account quotas are shared by all senders in this AWS account and region.</p>
      </CardHeader>
      <CardContent>
        {error && <p role="alert" className="mb-3 text-sm">{error.message}{data ? " Showing the last successful check." : ""}</p>}
        {refreshing && !data && <p role="status" className="muted text-sm">Loading Amazon SES status…</p>}
        {connection.isSuccess && !connection.data.has_credentials && <p className="muted text-sm">
          Add AWS access keys in <Link className="underline" to="/settings">Amazon SES settings</Link> to view sending status and quota.
        </p>}
        {data && <>
          {data.custom_endpoint && <p className="muted text-sm mb-3">A custom AWS endpoint is active. These figures are reported by that endpoint.</p>}
          <dl className="grid gap-4 text-sm sm:grid-cols-2 lg:grid-cols-4">
            <div><dt className="muted">Status</dt><dd className="mt-1"><Badge variant={health === "HEALTHY" ? "success" : health ? "warning" : "secondary"}>{healthLabel}</Badge></dd></div>
            <div><dt className="muted">Sending</dt><dd className="mt-1"><Badge variant={data.sending_enabled ? "success" : "destructive"}>{data.sending_enabled ? "Enabled" : "Disabled"}</Badge></dd></div>
            <div><dt className="muted">SES region</dt><dd className="mt-1 font-medium">{region}</dd></div>
            <div><dt className="muted">Account access</dt><dd className="mt-1 font-medium">{data.production_access_enabled ? "Production" : "Sandbox"}</dd></div>
            {[
              ["24-hour quota", data.quota.max_24_hour_send.toLocaleString()],
              ["Sends left", Math.max(0, data.quota.max_24_hour_send - data.quota.sent_last_24_hours).toLocaleString()],
              ["Sent in last 24 hours", data.quota.sent_last_24_hours.toLocaleString()],
              ["Sends per second", data.quota.max_send_rate.toLocaleString()],
            ].map(([label, value]) => <div key={label}><dt className="muted">{label}</dt><dd className="mt-1 text-lg font-semibold tabular-nums">{value}</dd></div>)}
          </dl>
          <p className="muted text-xs mt-4">Last checked: {data.checked_at ? new Date(data.checked_at).toLocaleString() : "Unavailable"}</p>
        </>}
      </CardContent>
    </Card>
  );
}
