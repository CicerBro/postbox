import { useEffect, useId, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { useApp } from "../../lib/app-context";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import { SelectField } from "../ui/select";
import { Field, Section, useAction } from "./Common";

type Settings = {
  mode: "static";
  region: string;
  configured: boolean;
  has_credentials: boolean;
  has_session_token: boolean;
  sns_topic_arn: string;
  updated_at?: number | string;
};
type Status = {
  custom_endpoint?: boolean;
  account_id?: string;
  region: string;
  sending_enabled: boolean;
  production_access_enabled: boolean;
  enforcement_status?: string;
  checked_at?: number | string;
  quota: {
    max_send_rate: number;
    max_24_hour_send: number;
    sent_last_24_hours: number;
  };
};

type EventStatus = {
  configured: boolean;
  configuration_set: string;
  sns_topic_arn: string;
  endpoint: string;
  subscription_status: "confirmed" | "pending_confirmation" | "not_subscribed";
  checked_at: number;
  connection_revision?: number | string;
};

// Amazon SES API endpoints: https://docs.aws.amazon.com/general/latest/gr/ses.html
const regions = [
  ["us-east-1", "US East (N. Virginia)"],
  ["us-east-2", "US East (Ohio)"],
  ["us-west-1", "US West (N. California)"],
  ["us-west-2", "US West (Oregon)"],
  ["af-south-1", "Africa (Cape Town)"],
  ["ap-south-2", "Asia Pacific (Hyderabad)"],
  ["ap-southeast-3", "Asia Pacific (Jakarta)"],
  ["ap-southeast-5", "Asia Pacific (Malaysia)"],
  ["ap-south-1", "Asia Pacific (Mumbai)"],
  ["ap-northeast-3", "Asia Pacific (Osaka)"],
  ["ap-northeast-2", "Asia Pacific (Seoul)"],
  ["ap-southeast-1", "Asia Pacific (Singapore)"],
  ["ap-southeast-2", "Asia Pacific (Sydney)"],
  ["ap-northeast-1", "Asia Pacific (Tokyo)"],
  ["ca-central-1", "Canada (Central)"],
  ["ca-west-1", "Canada West (Calgary)"],
  ["eu-central-1", "Europe (Frankfurt)"],
  ["eu-west-1", "Europe (Ireland)"],
  ["eu-west-2", "Europe (London)"],
  ["eu-south-1", "Europe (Milan)"],
  ["eu-west-3", "Europe (Paris)"],
  ["eu-north-1", "Europe (Stockholm)"],
  ["eu-central-2", "Europe (Zurich)"],
  ["il-central-1", "Israel (Tel Aviv)"],
  ["me-south-1", "Middle East (Bahrain)"],
  ["me-central-1", "Middle East (UAE)"],
  ["sa-east-1", "South America (São Paulo)"],
  ["us-gov-east-1", "AWS GovCloud (US-East)"],
  ["us-gov-west-1", "AWS GovCloud (US-West)"],
].map(([value, name]) => ({ value, label: `${name} (${value})` }));

function timestamp(value?: number | string) {
  if (!value) return "Not checked yet";
  return new Date(value).toLocaleString();
}

export function AmazonSES({ brandId }: { brandId: number }) {
  const { session } = useApp();
  const id = useId();
  const action = useAction();
  const queryClient = useQueryClient();
  const [eventStatus, setEventStatus] = useState<EventStatus>();
  const [region, setRegion] = useState("");
  const [topicArn, setTopicArn] = useState("");
  const [accessKey, setAccessKey] = useState("");
  const [secretKey, setSecretKey] = useState("");
  const [token, setToken] = useState("");
  const [clearToken, setClearToken] = useState(false);
  const [status, setStatus] = useState<Status>();
  const q = useQuery({
    queryKey: ["aws-settings", brandId],
    queryFn: () => api<Settings>("/aws-settings", { brandId }),
    enabled: brandId > 0,
  });
  useEffect(() => {
    if (q.data) {
      setRegion(q.data.region);
      setTopicArn(q.data.sns_topic_arn);
    }
  }, [q.data]);
  const dirty = region !== q.data?.region || topicArn !== q.data?.sns_topic_arn ||
    !!accessKey || !!secretKey || !!token || clearToken;
  const error = action.error || q.error;

  return (
    <div className="space-y-6">
      {error ? (
        <p role="alert" className="rounded-lg border p-3 text-sm">
          {error instanceof Error ? error.message : String(error)}
        </p>
      ) : null}
      {action.message && <p role="status" className="text-sm">{action.message}</p>}
      {q.isPending ? <p role="status">Loading Amazon SES settings…</p> :
        q.isError ? <Button variant="outline" onClick={() => q.refetch()}>Retry</Button> :
        q.data ? <>
          <Section title="Connection" description="Amazon SES sends from the AWS account and region configured for this brand.">
            <p className="text-sm text-muted-foreground">
              {q.data.configured ? "Access keys are configured for this brand." : "Add this brand's AWS access keys before sending."}
            </p>
            {session.admin ? (
              <form className="space-y-4" autoComplete="off" onSubmit={(e) => {
                e.preventDefault();
                void action.run(async () => {
                  await api("/aws-settings", {
                    method: "PATCH", brandId,
                    body: {
                      mode: "static", region, sns_topic_arn: topicArn,
                      ...(accessKey ? { access_key_id: accessKey } : {}),
                      ...(secretKey ? { secret_access_key: secretKey } : {}),
                      ...(token ? { session_token: token } : {}),
                      ...(clearToken ? { clear_session_token: true } : {}),
                    },
                  });
                  setAccessKey(""); setSecretKey(""); setToken(""); setClearToken(false);
                  setStatus(undefined);
                  setEventStatus(undefined);
                  await q.refetch();
                }, "Amazon SES settings saved. Test the connection to refresh the quota.");
              }}>
                <div className="space-y-2">
                  <label htmlFor={`${id}-region`} className="text-sm font-medium">AWS region</label>
                  <SelectField id={`${id}-region`} value={region} required disabled={action.busy}
                    onValueChange={(value) => { if (value) setRegion(value); }} placeholder="Select an AWS region"
                    options={region && !regions.some((option) => option.value === region)
                      ? [...regions, { value: region, label: region }] : regions} />
                  <p className="text-sm text-muted-foreground">SES quotas and verified identities belong to a region.</p>
                </div>
                <div className="space-y-4">
                  <p className="text-sm text-muted-foreground">
                    {q.data.has_credentials
                      ? "Access keys are saved. Leave these fields blank to keep them, or enter both keys to replace them."
                      : "Enter an AWS access key ID and secret access key. Use IAM access keys, not SES SMTP credentials."}
                  </p>
                  <Field label="Access key ID" type="password" autoComplete="new-password" value={accessKey}
                    required={!q.data.has_credentials || !!secretKey}
                    disabled={action.busy} onChange={(e) => setAccessKey(e.target.value)} />
                  <Field label="Secret access key" type="password" autoComplete="new-password" value={secretKey}
                    required={!q.data.has_credentials || !!accessKey}
                    disabled={action.busy} onChange={(e) => setSecretKey(e.target.value)} />
                  <Field label="Session token (optional)" type="password" autoComplete="new-password" value={token}
                    disabled={action.busy || clearToken} onChange={(e) => setToken(e.target.value)} />
                  <p className="text-sm text-muted-foreground">
                    {q.data.has_session_token ? "A session token is saved. Leave blank to keep it." : "Use a session token only for temporary AWS credentials."}
                  </p>
                  {q.data.has_session_token && <label htmlFor={`${id}-clear-token`} className="flex items-center gap-2 text-sm">
                    <Checkbox id={`${id}-clear-token`} checked={clearToken} disabled={action.busy}
                      onCheckedChange={(value) => { setClearToken(value === true); setToken(""); }} />
                    Remove saved session token
                  </label>}
                </div>
                <div className="space-y-4">
                  <h3 className="text-sm font-medium">SES events</h3>
                  <p className="text-sm text-muted-foreground">Leave the topic ARN blank to create a topic during setup, or enter an existing Standard topic owned by this AWS account in the selected region.</p>
                  <Field label="SNS topic ARN" type="text" value={topicArn}
                    disabled={action.busy} onChange={(e) => setTopicArn(e.target.value)} />
                </div>
                <Button disabled={action.busy || !region}>Save Amazon SES settings</Button>
              </form>
            ) : <dl className="grid gap-3 text-sm sm:grid-cols-2">
              <div><dt className="text-muted-foreground">Access keys</dt><dd>{q.data.has_credentials ? "Configured" : "Not configured"}</dd></div>
              <div><dt className="text-muted-foreground">AWS region</dt><dd>{q.data.region || "Not configured"}</dd></div>
              <div><dt className="text-muted-foreground">SNS topic ARN</dt><dd>{q.data.sns_topic_arn || "Not configured"}</dd></div>
            </dl>}
            <div className="space-y-3">
              <h3 className="text-sm font-medium">Event delivery</h3>
              <p className="text-sm text-muted-foreground">Set up delivery, bounce and complaint events. Postbox creates the SNS topic, publish policy, HTTPS subscription and SES configuration set. Existing saved resources are reused.</p>
              <div className="flex flex-wrap gap-2">
                {session.admin && <Button variant="outline" disabled={action.busy || dirty || !q.data.has_credentials} onClick={() => {
                  setEventStatus(undefined);
                  void action.run(async () => {
                    const result = await api<EventStatus>("/aws-settings/setup-events", { method: "POST", brandId }).finally(async () => {
                      // A partial setup can still have saved a topic and configuration set.
                      await q.refetch();
                      await queryClient.invalidateQueries({ queryKey: ["brands"] });
                      await queryClient.invalidateQueries({ queryKey: ["brand-settings", brandId] });
                    });
                    const saved = queryClient.getQueryData<Settings>(["aws-settings", brandId]);
                    setEventStatus({ ...result, connection_revision: saved?.updated_at });
                  }, "SES and SNS resources configured. Check the subscription status below.");
                }}>Set up SES and SNS</Button>}
                <Button variant="outline" disabled={action.busy || !q.data.has_credentials || (session.admin && dirty)} onClick={() => {
                  setEventStatus(undefined);
                  void action.run(async () => {
                    const result = await api<EventStatus>("/aws-settings/event-status", { brandId });
                    setEventStatus({ ...result, connection_revision: q.data?.updated_at });
                  }, "Event status refreshed.");
                }}>Refresh event status</Button>
              </div>
              {session.admin && dirty && <p className="text-sm text-muted-foreground">Save your changes before setting up or checking events.</p>}
              {session.admin && <p className="text-sm text-muted-foreground">The server needs a public HTTPS base URL, and the saved keys need the SES and SNS setup permissions listed in the handbook.</p>}
              {eventStatus && eventStatus.connection_revision === q.data.updated_at && <div className="space-y-3 rounded-lg border p-3 text-sm">
                <p role="status">{!eventStatus.configured ? "SES event destination is not configured." : eventStatus.subscription_status === "confirmed" ? "Event delivery is ready. SNS subscription confirmed." : eventStatus.subscription_status === "pending_confirmation" ? "Waiting for SNS subscription confirmation. Ensure the public endpoint is reachable, then refresh event status." : "SNS endpoint is not subscribed. Run setup to connect it."}</p>
                <dl className="grid gap-3 sm:grid-cols-2">
                  <div><dt className="text-muted-foreground">Configuration set</dt><dd>{eventStatus.configuration_set || "Not configured"}</dd></div>
                  <div><dt className="text-muted-foreground">Subscription</dt><dd>{eventStatus.subscription_status.replaceAll("_", " ")}</dd></div>
                  <div className="sm:col-span-2"><dt className="text-muted-foreground">SNS topic</dt><dd className="break-all">{eventStatus.sns_topic_arn || "Not configured"}</dd></div>
                  <div className="sm:col-span-2"><dt className="text-muted-foreground">HTTPS endpoint</dt><dd className="break-all">{eventStatus.endpoint}</dd></div>
                  <div><dt className="text-muted-foreground">Last checked</dt><dd>{timestamp(eventStatus.checked_at)}</dd></div>
                </dl>
              </div>}
            </div>
          </Section>
          <Section title="Sending status" description="Live account statistics reported by AWS for the saved connection. Quotas are shared by all senders in this AWS account and region.">
            <Button variant="outline" disabled={action.busy || !q.data.has_credentials || (session.admin && dirty)} onClick={() => {
              void action.run(async () => {
                const result = await api<Status>("/aws-settings/check", { method: "POST", brandId });
                setStatus(result);
              }, "Connection checked. Sending quota refreshed.");
            }}>{action.busy ? "Working…" : status ? "Refresh quota" : "Test connection"}</Button>
            {session.admin && dirty && <p className="text-sm text-muted-foreground">Save your changes before testing the connection.</p>}
            {status?.custom_endpoint && <p role="status" className="rounded-lg border p-3 text-sm">A custom AWS endpoint is active. These figures are reported by that endpoint.</p>}
            {!status ? <p className="text-sm text-muted-foreground">{q.data.has_credentials ? "Test the saved connection to fetch current AWS sending limits and account status." : "Save this brand's access keys to test the connection."}</p> : <dl className="grid gap-4 text-sm sm:grid-cols-2 lg:grid-cols-3">
              {[
                ["AWS region", status.region],
                ["AWS account", status.account_id || "Unavailable"],
                ["Account access", status.production_access_enabled ? "Production" : "Sandbox"],
                ["Sending", status.sending_enabled ? "Enabled" : "Disabled"],
                ["Maximum sends per second", status.quota.max_send_rate.toLocaleString()],
                ["24-hour sending quota", status.quota.max_24_hour_send.toLocaleString()],
                ["Sent in the last 24 hours", status.quota.sent_last_24_hours.toLocaleString()],
                ["Remaining 24-hour quota", Math.max(0, status.quota.max_24_hour_send - status.quota.sent_last_24_hours).toLocaleString()],
                ["Last checked", timestamp(status.checked_at)],
                ...(status.enforcement_status ? [["Enforcement status", status.enforcement_status]] : []),
              ].map(([label, value]) => <div key={label}><dt className="text-muted-foreground">{label}</dt><dd className="mt-1 font-medium">{value}</dd></div>)}
            </dl>}
          </Section>
        </> : null}
    </div>
  );
}
