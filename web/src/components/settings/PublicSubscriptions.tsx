import { useId } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { useApp } from "../../lib/app-context";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import { Message, Section, useAction } from "./Common";

type Settings = {
  public_subscriptions_enabled: boolean;
  application_enabled: boolean;
  enabled: boolean;
};

export function PublicSubscriptions({ brandId }: { brandId: number }) {
  const { session } = useApp();
  const id = useId();
  const qc = useQueryClient();
  const action = useAction();
  const app = useQuery({
    queryKey: ["application-settings"],
    queryFn: () => api<Pick<Settings, "public_subscriptions_enabled">>("/application-settings"),
    enabled: session.admin,
  });
  const brand = useQuery({
    queryKey: ["subscription-settings", brandId],
    queryFn: () => api<Settings>("/subscription-settings", { brandId }),
    enabled: brandId > 0,
  });
  async function save(application: boolean, enabled: boolean) {
    await action.run(async () => {
      await api(application ? "/application-settings" : "/subscription-settings", {
        method: "PATCH",
        ...(application ? {} : { brandId }),
        body: { public_subscriptions_enabled: enabled },
      });
      await Promise.all([
        qc.invalidateQueries({ queryKey: ["application-settings"] }),
        qc.invalidateQueries({ queryKey: ["subscription-settings"] }),
      ]);
    }, "Public subscription settings saved.");
  }
  const error = action.error || (session.admin && app.error) || (brandId > 0 && brand.error);
  return (
    <div className="space-y-6">
      <p className="text-sm text-muted-foreground">
        Hosted signup pages and embedded forms accept subscriptions when both
        switches are on. Confirmation and unsubscribe links remain available.
        Both switches are on by default.
      </p>
      {session.admin && (
        <Section title="Application" description="Control public signup for every brand.">
          {app.isPending ? <p role="status">Loading application settings…</p> : app.isError ? (
            <Button variant="outline" onClick={() => app.refetch()}>Retry application settings</Button>
          ) : app.data && (
            <label htmlFor={`${id}-application`} className="flex items-center gap-3 text-sm">
              <Checkbox id={`${id}-application`} checked={app.data.public_subscriptions_enabled}
                disabled={action.busy} onCheckedChange={(value) => void save(true, value === true)} />
              Allow public subscriptions across the application
            </label>
          )}
        </Section>
      )}
      {brandId > 0 && (
        <Section title="Selected brand" description="Control public signup for this brand.">
          {brand.isPending ? <p role="status">Loading brand settings…</p> : brand.isError ? (
            <Button variant="outline" onClick={() => brand.refetch()}>Retry brand settings</Button>
          ) : brand.data && <>
            <label htmlFor={`${id}-brand`} className="flex items-center gap-3 text-sm">
              <Checkbox id={`${id}-brand`} checked={brand.data.public_subscriptions_enabled}
                disabled={action.busy} onCheckedChange={(value) => void save(false, value === true)} />
              Allow public subscriptions for this brand
            </label>
            <p role="status" className="text-sm text-muted-foreground">
              {brand.data.enabled ? "Public signup is enabled for this brand." :
                !brand.data.application_enabled ? "Public signup is disabled across the application." :
                  "Public signup is disabled for this brand."}
            </p>
          </>}
        </Section>
      )}
      <Message error={error} message={action.message} />
    </div>
  );
}
