import { useDateTime } from "../../lib/date-time";
import { useEffect, useId, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Copy, RefreshCw } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { useApp } from "../../lib/app-context";
import { Button } from "../ui/button";
import { SelectField } from "../ui/select";
import { Field, Message, Section, useAction } from "./Common";

type DNSRecord = { type: string; name: string; value: string; priority?: number };
type SESIdentity = {
  identity: string;
  identity_type: string;
  region: string;
  custom_endpoint?: boolean;
  sending_enabled: boolean;
  verification_status: string;
  dkim_status?: string;
  dkim_origin?: string;
  dkim_hosted_zone?: string;
  dkim_signing_enabled?: boolean;
  mail_from_domain?: string;
  mail_from_status?: string;
  mail_from_behavior?: string;
  checked_at: number;
  dns_records: DNSRecord[];
};

function statusLabel(status?: string) {
  switch (status) {
    case "SUCCESS": return "Verified";
    case "PENDING": return "Pending DNS verification";
    case "FAILED": return "Verification failed";
    case "TEMPORARY_FAILURE": return "Temporary verification failure";
    case "NOT_STARTED": return "Verification not started";
    default: return status || "Not reported by AWS";
  }
}

export function SESDomain({ brandId }: { brandId: number }) {
  const brand = useQuery({
    queryKey: ["brand-settings", brandId],
    queryFn: () => api<{ from_email: string }[]>(`/brands/${brandId}`, { brandId }),
  });
  const settings = useQuery({
    queryKey: ["aws-settings", brandId],
    queryFn: () => api<{ has_credentials: boolean; region: string; updated_at: number }>("/aws-settings", { brandId }),
  });
  const senderDomain = brand.data?.[0]?.from_email.split("@").pop() || "";
  const [input, setInput] = useState<string>();
  const [selected, setSelected] = useState<string>();
  const domain = selected ?? senderDomain;
  return (
    <Section title="SES domain verification" description="Verify your sender domain and view DNS records in the AWS account and region saved for this brand.">
      <Message error={brand.error || settings.error} />
      {brand.isPending || settings.isPending ? <p role="status">Loading domain settings…</p> :
        settings.data?.has_credentials ? <>
          <form className="flex flex-wrap items-end gap-3" onSubmit={(e) => {
            e.preventDefault();
            setSelected((input ?? senderDomain).trim().toLowerCase().replace(/\.$/, ""));
          }}>
            <div className="min-w-0 flex-1">
              <Field label="Domain to verify" placeholder="example.com" required maxLength={253}
                value={input ?? senderDomain} onChange={(e) => setInput(e.target.value)} />
            </div>
            <Button variant="outline">View domain</Button>
          </form>
          <p className="text-sm text-muted-foreground">AWS region: {settings.data.region}. You can enter a parent domain if your sender uses a subdomain.</p>
          {domain && <DomainIdentity key={`${brandId}:${domain}:${settings.data.updated_at}:${settings.data.region}`} brandId={brandId} domain={domain} connectionVersion={settings.data.updated_at} region={settings.data.region} />}
        </> : settings.data ? <p className="text-sm text-muted-foreground">Save this brand’s AWS access keys in Sending &amp; domains to verify its domain.</p> : null}
    </Section>
  );
}

function DomainIdentity({ brandId, domain, connectionVersion, region }: { brandId: number; domain: string; connectionVersion: number; region: string }) {
  const { formatDateTime } = useDateTime();
  const { session } = useApp();
  const qc = useQueryClient();
  const action = useAction();
  const copy = useAction();
  const fieldId = useId();
  const [mailFrom, setMailFrom] = useState("");
  const [behavior, setBehavior] = useState("USE_DEFAULT_VALUE");
  const queryKey = ["ses-identity", brandId, domain, connectionVersion, region];
  const q = useQuery({
    queryKey,
    queryFn: ({ signal }) => api<SESIdentity>(`/aws-settings/identity?identity=${encodeURIComponent(domain)}`, { brandId, signal }),
    retry: false,
    refetchOnWindowFocus: false,
  });
  useEffect(() => {
    if (q.data) {
      setMailFrom(q.data.mail_from_domain || "");
      setBehavior(q.data.mail_from_behavior || "USE_DEFAULT_VALUE");
    }
  }, [q.data]);
  const missing = q.error instanceof ApiError && q.error.status === 404;
  const denied = q.error instanceof ApiError && q.error.status === 403;
  const identity = missing || denied ? undefined : q.data;
  const renderDNSRecord = (record: DNSRecord) => <div key={`${record.type}:${record.name}`} className="space-y-3 rounded-lg bg-muted/50 p-3 text-sm">
    <div className="font-medium">{record.type === "CNAME" ? "DKIM" : "MAIL FROM"} · {record.type}{record.priority !== undefined ? ` · Priority ${record.priority}` : ""}</div>
    {[["Name", record.name], ["Value", record.value]].map(([label, value]) => <div key={label} className="flex items-start gap-2">
      <div className="min-w-0 flex-1"><span className="text-muted-foreground">{label}</span><code className="block break-all">{value}</code></div>
      <Button variant="outline" size="sm" aria-label={`Copy ${record.type} ${label.toLowerCase()} for ${record.name}`} onClick={() => void copy.run(() => navigator.clipboard.writeText(value), `${label} copied.`)}>
        <Copy size={14} aria-hidden="true" /> Copy
      </Button>
    </div>)}
  </div>;
  return (
    <div className="space-y-4 rounded-lg border p-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h4 className="break-all font-medium">{identity?.identity || domain}</h4>
        <Button variant="outline" disabled={q.isFetching || action.busy} onClick={() => void q.refetch()}>
          <RefreshCw size={14} aria-hidden="true" /> Refresh status
        </Button>
      </div>
      {q.isPending && <p role="status">Checking SES identity…</p>}
      {missing ? <>
        <p className="text-sm">This domain has no SES identity in this brand’s AWS account and region.</p>
        {session.admin ? <Button disabled={action.busy || q.isFetching} onClick={() => {
          void action.run(async () => {
            const result = await api<SESIdentity>("/aws-settings/identity", { method: "POST", brandId, body: { identity: domain } });
            qc.setQueryData(queryKey, result);
          }, "SES identity created or loaded. Publish its DNS records, then refresh the status.");
        }}>Verify domain</Button> : <p className="text-sm text-muted-foreground">An administrator can start domain verification.</p>}
      </> : <Message error={q.error} />}
      {identity && q.isError && <p className="text-sm text-muted-foreground">The details below are from the last successful check. Refresh status to try again.</p>}
      {identity && <>
        {identity.custom_endpoint && <p role="status" className="rounded-lg bg-muted p-3 text-sm">A custom AWS endpoint is active. This status and these records are reported by that endpoint.</p>}
        <dl className="grid gap-4 text-sm sm:grid-cols-2 lg:grid-cols-3">
          {[
            ["Verification", statusLabel(identity.verification_status)],
            ["Verified for sending", identity.sending_enabled ? "Yes" : "No"],
            ["DKIM", statusLabel(identity.dkim_status)],
            ["DKIM signing", identity.dkim_signing_enabled ? "Enabled" : "Disabled"],
            ["MAIL FROM", identity.mail_from_domain ? statusLabel(identity.mail_from_status) : "Default SES domain"],
            ["Last checked", formatDateTime(identity.checked_at)],
          ].map(([label, value]) => <div key={label}><dt className="text-muted-foreground">{label}</dt><dd className="mt-1 font-medium">{value}</dd></div>)}
        </dl>
        <div className="space-y-3">
          <h4 className="font-medium">DNS records</h4>
          <p className="text-sm text-muted-foreground">Add these records at your DNS provider. AWS verifies them automatically; detection can take up to 72 hours. These are full hostnames, so avoid adding your domain twice if your provider appends it.</p>
          {identity.dns_records.some((record) => record.type === "CNAME") && <details open={!(identity.dkim_status === "SUCCESS" && identity.dkim_signing_enabled)} className="space-y-3">
            <summary className="cursor-pointer font-medium">DKIM DNS records</summary>
            <div className="space-y-3">
              {identity.dns_records.filter((record) => record.type === "CNAME").map(renderDNSRecord)}
            </div>
          </details>}
          {identity.dns_records.some((record) => record.type !== "CNAME") && <details open={identity.mail_from_status !== "SUCCESS"} className="space-y-3">
            <summary className="cursor-pointer font-medium">MAIL FROM DNS records</summary>
            <div className="space-y-3">
              {identity.dns_records.filter((record) => record.type !== "CNAME").map(renderDNSRecord)}
              {identity.mail_from_domain && <p className="text-sm text-muted-foreground">For MAIL FROM, publish exactly one MX record with priority 10 and one SPF TXT record. Use a dedicated subdomain that does not receive mail.</p>}
            </div>
          </details>}
          {identity.dkim_origin === "EXTERNAL" ? <p className="text-sm text-muted-foreground">This identity uses Bring Your Own DKIM. Publish the TXT record for your own public key; Postbox cannot derive that key from SES.</p> :
            identity.dkim_origin?.startsWith("AWS_SES_") ? <p className="text-sm text-muted-foreground">This identity uses Deterministic Easy DKIM. Its parent identity supplies DKIM; no additional DKIM records are required here.</p> :
            !identity.dns_records.some((record) => record.type === "CNAME") ? <p className="text-sm text-muted-foreground">AWS did not return Easy DKIM DNS records. Check the identity’s authentication settings in Amazon SES.</p> : null}
          <Message {...copy} />
        </div>
        {session.admin && <form className="space-y-4 border-t pt-4" onSubmit={(e) => {
          e.preventDefault();
          void action.run(async () => {
            await api("/aws-settings/identity/mail-from", { method: "PATCH", brandId, body: { identity: identity.identity, mail_from_domain: mailFrom.trim(), mail_from_behavior: behavior } });
            await q.refetch();
          }, mailFrom.trim() ? "MAIL FROM settings saved. Publish its MX and SPF records, then refresh the status." : "Custom MAIL FROM disabled. SES will use its default MAIL FROM domain.");
        }}>
          <h4 className="font-medium">Custom MAIL FROM domain</h4>
          <Field label="MAIL FROM subdomain" placeholder={`bounce.${identity.identity}`} maxLength={253} value={mailFrom} disabled={action.busy} onChange={(e) => setMailFrom(e.target.value)} />
          <p className="text-sm text-muted-foreground">Use a subdomain of {identity.identity}. Leave blank to use the default SES MAIL FROM domain.</p>
          <div className="space-y-2">
            <label htmlFor={fieldId} className="text-sm font-medium">If the MX record cannot be verified</label>
            <SelectField id={fieldId} value={behavior} onValueChange={setBehavior} disabled={action.busy} options={[
              { value: "USE_DEFAULT_VALUE", label: "Use the default SES MAIL FROM domain" },
              { value: "REJECT_MESSAGE", label: "Reject sending" },
            ]} />
          </div>
          <Button disabled={action.busy || q.isFetching || !identity.sending_enabled}>Save MAIL FROM settings</Button>
          {!identity.sending_enabled && <p className="text-sm text-muted-foreground">Verify this domain before configuring MAIL FROM.</p>}
        </form>}
      </>}
      <Message {...action} />
    </div>
  );
}
