import { useEffect, useId, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { useApp } from "../../lib/app-context";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import { SelectField } from "../ui/select";
import { Field, Section, useAction } from "./Common";

type Mode = "static" | "instance_role";
type Settings = {
  mode: Mode | "server";
  region: string;
  configured: boolean;
  has_credentials: boolean;
  has_session_token: boolean;
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
  const [mode, setMode] = useState<Mode>("instance_role");
  const [region, setRegion] = useState("");
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
      setMode(q.data.mode === "server" ? "instance_role" : q.data.mode);
      setRegion(q.data.region);
    }
  }, [q.data]);
  const dirty = mode !== (q.data?.mode === "server" ? "instance_role" : q.data?.mode) || region !== q.data?.region ||
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
              {q.data.configured ? "This brand has its own AWS configuration." : "This brand currently uses the server AWS configuration."}
            </p>
            {session.admin ? (
              <form className="space-y-4" autoComplete="off" onSubmit={(e) => {
                e.preventDefault();
                void action.run(async () => {
                  await api("/aws-settings", {
                    method: "PATCH", brandId,
                    body: {
                      mode, region,
                      ...(mode === "static" ? {
                        ...(accessKey ? { access_key_id: accessKey } : {}),
                        ...(secretKey ? { secret_access_key: secretKey } : {}),
                        ...(token ? { session_token: token } : {}),
                        ...(clearToken ? { clear_session_token: true } : {}),
                      } : {}),
                    },
                  });
                  setAccessKey(""); setSecretKey(""); setToken(""); setClearToken(false);
                  setStatus(undefined);
                  await q.refetch();
                }, "Amazon SES settings saved. Test the connection to refresh the quota.");
              }}>
                <div className="space-y-2">
                  <label htmlFor={`${id}-mode`} className="text-sm font-medium">Authentication</label>
                  <SelectField id={`${id}-mode`} value={mode} disabled={action.busy}
                    onValueChange={(value) => {
                      setMode(value as Mode); setAccessKey(""); setSecretKey(""); setToken(""); setClearToken(false);
                    }} options={[
                      { value: "static", label: "Static access keys" },
                      { value: "instance_role", label: "Server AWS credentials (IAM role)" },
                    ]} />
                </div>
                <div className="space-y-2">
                  <label htmlFor={`${id}-region`} className="text-sm font-medium">AWS region</label>
                  <SelectField id={`${id}-region`} value={region} required disabled={action.busy}
                    onValueChange={setRegion} placeholder="Select an AWS region"
                    options={region && !regions.some((option) => option.value === region)
                      ? [...regions, { value: region, label: region }] : regions} />
                  <p className="text-sm text-muted-foreground">SES quotas and verified identities belong to a region.</p>
                </div>
                {mode === "static" ? <div className="space-y-4">
                  <p className="text-sm text-muted-foreground">
                    {q.data.mode === "static" && q.data.has_credentials
                      ? "Access keys are saved. Leave these fields blank to keep them, or enter both keys to replace them."
                      : "Enter an AWS access key ID and secret access key. Use IAM access keys, not SES SMTP credentials."}
                  </p>
                  <Field label="Access key ID" type="password" autoComplete="new-password" value={accessKey}
                    required={!q.data.has_credentials || q.data.mode !== "static" || !!secretKey}
                    disabled={action.busy} onChange={(e) => setAccessKey(e.target.value)} />
                  <Field label="Secret access key" type="password" autoComplete="new-password" value={secretKey}
                    required={!q.data.has_credentials || q.data.mode !== "static" || !!accessKey}
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
                </div> : <p className="text-sm text-muted-foreground">
                  The server uses its configured AWS credentials, such as an IAM role, to access Amazon SES. No access keys are needed here.
                </p>}
                <Button disabled={action.busy || !region}>Save Amazon SES settings</Button>
              </form>
            ) : <dl className="grid gap-3 text-sm sm:grid-cols-2">
              <div><dt className="text-muted-foreground">Authentication</dt><dd>{q.data.mode === "static" ? "Static access keys" : q.data.mode === "server" ? "Server AWS configuration" : "Server AWS credentials (IAM role)"}</dd></div>
              <div><dt className="text-muted-foreground">AWS region</dt><dd>{q.data.region || "Not configured"}</dd></div>
            </dl>}
          </Section>
          <Section title="Sending status" description="Live account statistics reported by AWS for the saved connection. Quotas are shared by all senders in this AWS account and region.">
            <Button variant="outline" disabled={action.busy || (session.admin && dirty)} onClick={() => {
              void action.run(async () => {
                const result = await api<Status>("/aws-settings/check", { method: "POST", brandId });
                setStatus(result);
              }, "Connection checked. Sending quota refreshed.");
            }}>{action.busy ? "Working…" : status ? "Refresh quota" : "Test connection"}</Button>
            {session.admin && dirty && <p className="text-sm text-muted-foreground">Save your changes before testing the connection.</p>}
            {status?.custom_endpoint && <p role="status" className="rounded-lg border p-3 text-sm">A custom AWS endpoint is active. These figures are reported by that endpoint.</p>}
            {!status ? <p className="text-sm text-muted-foreground">Test the saved connection to fetch current AWS sending limits and account status.</p> : <dl className="grid gap-4 text-sm sm:grid-cols-2 lg:grid-cols-3">
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
