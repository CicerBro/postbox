import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useId, useState } from "react";
import { api } from "../../lib/api";
import { useApp } from "../../lib/app-context";
import { Button } from "../ui/button";
import { InputField } from "../ui/field";
import { Switch } from "../ui/switch";
import { Section, Message, useAction } from "./Common";

export function SendingControl() {
  const { session } = useApp();
  const id = useId();
  const qc = useQueryClient();
  const action = useAction();
  const q = useQuery({
    queryKey: ["sending"],
    queryFn: () => api<{ enabled: boolean }>("/sending"),
    enabled: session.admin,
  });
  return (
    <Section
      title="Sending across all brands"
      description="Pause or resume application-wide email delivery."
    >
      <Message error={q.error || action.error} message={action.message} />
      {q.isPending ? (
        <p role="status">Loading sending status…</p>
      ) : q.isError ? (
        <Button variant="outline" onClick={() => q.refetch()}>
          Retry
        </Button>
      ) : (
        q.data && (
          <label htmlFor={id} className="flex items-center gap-3 text-sm">
            <Switch
              id={id}
              checked={q.data.enabled}
              disabled={action.busy}
              onCheckedChange={(enabled) =>
                void action.run(
                  async () => {
                    await api("/sending", {
                      method: "POST",
                      body: { enabled },
                    });
                    await qc.invalidateQueries({ queryKey: ["sending"] });
                  },
                  enabled
                    ? "Sending resumed across all brands."
                    : "Sending paused across all brands.",
                )
              }
            />
            Enable sending across all brands
          </label>
        )
      )}
    </Section>
  );
}

export function GmailSendingLimit() {
  const helpID = useId();
  const [value, setValue] = useState<string>();
  const qc = useQueryClient();
  const action = useAction();
  const queryKey = ["sending-settings"];
  type SendingSettings = { gmail_sends_per_minute: number };
  const settings = useQuery({
    queryKey,
    queryFn: () => api<SendingSettings>("/sending-settings"),
  });
  const saved = settings.data?.gmail_sends_per_minute;
  return (
    <Section title="Gmail campaign pacing">
      <form
        className="max-w-lg space-y-3"
        onSubmit={(event) => {
          event.preventDefault();
          if (value === undefined) return;
          void action.run(async () => {
            const updated = await api<SendingSettings>("/sending-settings", {
              method: "PATCH",
              body: { gmail_sends_per_minute: Number(value) },
            });
            qc.setQueryData(queryKey, updated);
            setValue(undefined);
          }, "Gmail send limit saved.");
        }}
      >
        <InputField
          label="Gmail sends per minute"
          type="number"
          min={0}
          max={60000}
          step={1}
          required
          value={value ?? saved ?? ""}
          onChange={(event) => setValue(event.target.value)}
          disabled={saved === undefined || action.busy}
          aria-describedby={helpID}
        />
        <p id={helpID} className="text-sm text-muted-foreground">
          Applies to campaign sends to gmail.com and googlemail.com across all
          brands. Set 0 to disable the Gmail cap. Changes apply to active
          campaigns now and to paused campaigns when they resume.
        </p>
        <Button
          disabled={
            saved === undefined ||
            value === undefined ||
            value === String(saved) ||
            action.busy
          }
        >
          {action.busy ? "Saving…" : "Save Gmail limit"}
        </Button>
        {settings.isError && (
          <Button
            type="button"
            variant="outline"
            onClick={() => settings.refetch()}
          >
            Retry
          </Button>
        )}
        <Message error={settings.error ?? action.error} message={action.message} />
      </form>
    </Section>
  );
}

type Deployment = {
  base_url: string;
  secure_cookies: boolean;
  session_ttl_seconds: number;
  turnstile_configured: boolean;
  trusted_proxy_count: number;
  backups_configured: boolean;
  raw_event_retention_days: number;
  max_send_workers: number;
  recovery_policy: string;
  soft_bounce_limit: number;
  geoip_configured: boolean;
};
export function DeploymentSettings() {
  const { session } = useApp();
  const q = useQuery({
    queryKey: ["deployment-settings"],
    queryFn: () => api<Deployment>("/deployment-settings"),
    enabled: session.admin,
  });
  const labels: Record<keyof Deployment, string> = {
    base_url: "Public base URL",
    secure_cookies: "Secure cookies",
    session_ttl_seconds: "Session lifetime (seconds)",
    turnstile_configured: "Turnstile configured",
    trusted_proxy_count: "Trusted proxies",
    backups_configured: "Backups configured",
    raw_event_retention_days: "Raw event retention (days)",
    max_send_workers: "Send workers",
    recovery_policy: "Recovery policy",
    soft_bounce_limit: "Soft bounce limit",
    geoip_configured: "GeoIP configured",
  };
  return (
    <Section
      title="Deployment configuration"
      description="These settings are managed through server environment variables. Update the deployment configuration and restart Postbox to change them."
    >
      <Message error={q.error} />
      {q.isPending ? (
        <p role="status">Loading deployment configuration…</p>
      ) : q.isError ? (
        <Button variant="outline" onClick={() => q.refetch()}>
          Retry
        </Button>
      ) : (
        q.data && (
          <dl className="grid gap-4 text-sm sm:grid-cols-2">
            {(Object.keys(labels) as (keyof Deployment)[]).map((key) => (
              <div key={key}>
                <dt className="text-muted-foreground">{labels[key]}</dt>
                <dd className="break-all font-medium">
                  {typeof q.data[key] === "boolean"
                    ? q.data[key]
                      ? "Yes"
                      : "No"
                    : String(q.data[key] ?? "Not configured") ||
                      "Not configured"}
                </dd>
              </div>
            ))}
          </dl>
        )
      )}
    </Section>
  );
}
