import { Send } from "lucide-react";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Textarea } from "../components/ui/textarea";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "../components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "../components/ui/dialog";
import { bytes, ErrorMessage, number, tableClass } from "./campaign-shared";
import type { Checklist } from "./campaign-checklist";

export type CampaignConfirmation =
  | "start"
  | "schedule"
  | "cancel"
  | "delete"
  | null;
type DeliveryProps = {
  editable: boolean;
  busy: boolean;
  dirty: boolean;
  canSend: boolean;
  checking: boolean;
  status: string;
  schedule: string;
  testEmails: string;
  checklist: Checklist | undefined;
  checklistError: unknown;
  setSchedule: (value: string) => void;
  setTestEmails: (value: string) => void;
  setConfirm: (value: CampaignConfirmation) => void;
  preflight: () => void;
  sendingConfirmation: (mode: "start" | "schedule") => void;
  action: (name: string, body?: unknown) => void;
};
export function CampaignDelivery({
  editable,
  busy,
  dirty,
  canSend,
  checking,
  status,
  schedule,
  testEmails,
  checklist,
  checklistError,
  setSchedule,
  setTestEmails,
  setConfirm,
  preflight,
  sendingConfirmation,
  action,
}: DeliveryProps) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Send size={18} /> Delivery
        </CardTitle>
        <p className="row-subtitle">
          Review your campaign, send a test, and choose when to deliver.
        </p>
      </CardHeader>
      <CardContent className="space-y-4">
        {editable && (
          <>
            <div className="flex flex-wrap gap-2">
              <Button
                variant="outline"
                onClick={preflight}
                disabled={checking || busy || dirty}
              >
                {checking ? "Checking…" : "Run preflight"}
              </Button>
              <Button
                onClick={() => sendingConfirmation("start")}
                disabled={busy || dirty || !canSend || checking}
              >
                Start sending
              </Button>
            </div>
            <div className="flex flex-wrap items-end gap-2">
              <label className="text-sm">
                Schedule in {Intl.DateTimeFormat().resolvedOptions().timeZone}
                <Input
                  type="datetime-local"
                  value={schedule}
                  onChange={(e) => setSchedule(e.target.value)}
                />
              </label>
              <Button
                variant="outline"
                onClick={() => sendingConfirmation("schedule")}
                disabled={busy || dirty || !canSend || checking}
              >
                Schedule
              </Button>
            </div>
          </>
        )}
        {["sending", "preparing"].includes(status) && (
          <Button
            variant="outline"
            disabled={busy}
            onClick={() => action("pause")}
          >
            Pause sending
          </Button>
        )}
        {status === "paused" && (
          <Button disabled={busy || !canSend} onClick={() => action("resume")}>
            Resume sending
          </Button>
        )}
        {["scheduled", "preparing", "sending", "paused"].includes(status) && (
          <Button
            variant="destructive"
            disabled={busy}
            onClick={() => setConfirm("cancel")}
          >
            Cancel campaign
          </Button>
        )}
        {checklist && <Preflight data={checklist} />}
        <ErrorMessage error={checklistError} />
        <div className="border-t pt-4">
          <label className="block text-sm">
            Send deliberate [Test] emails
            <Textarea
              value={testEmails}
              onChange={(e) => setTestEmails(e.target.value)}
              placeholder="one@example.com, two@example.com"
              className="mt-2"
            />
          </label>
          <p className="my-2 text-sm text-muted-foreground">
            Test sends are intentionally repeatable and use the saved campaign
            with a [Test] subject prefix. Each click sends again.
          </p>
          <Button
            variant="info"
            disabled={busy || dirty || !canSend || !testEmails.trim()}
            onClick={() =>
              action("test-send", {
                emails: testEmails.split(/[\s,;]+/).filter(Boolean),
              })
            }
          >
            <Send size={16} /> Send [Test] emails
          </Button>
        </div>
        {editable && (
          <div className="border-t pt-4">
            <Button
              variant="destructive"
              disabled={busy}
              onClick={() => setConfirm("delete")}
            >
              Delete draft
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

type ConfirmationProps = {
  confirm: CampaignConfirmation;
  busy: boolean;
  schedule: string;
  checklist: Checklist | undefined;
  error: unknown;
  setConfirm: (value: CampaignConfirmation) => void;
  confirmed: () => void;
};
export function CampaignConfirmationDialog({
  confirm,
  busy,
  schedule,
  checklist,
  error,
  setConfirm,
  confirmed,
}: ConfirmationProps) {
  return (
    <Dialog
      open={confirm !== null}
      onOpenChange={(open) => {
        if (!open && !busy) setConfirm(null);
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {confirm === "start"
              ? "Start campaign?"
              : confirm === "schedule"
                ? "Schedule campaign?"
                : confirm === "delete"
                  ? "Delete draft?"
                  : "Cancel campaign?"}
          </DialogTitle>
        </DialogHeader>
        <p className="text-sm">
          {confirm === "delete"
            ? "This permanently deletes this draft and its audience selection."
            : confirm === "cancel"
              ? "Stop sending this campaign. Messages already accepted by SES cannot be recalled."
              : `Send the saved campaign to ${number(checklist?.recipients ?? 0)} recipients${confirm === "schedule" ? ` at ${new Date(schedule).toLocaleString()}` : " now"}.`}
        </p>
        {(confirm === "start" || confirm === "schedule") &&
          checklist?.checks.warnings?.map((w, i) => (
            <p key={i} className="text-sm">
              Warning: {w.message}
            </p>
          ))}
        <ErrorMessage error={error} />
        <DialogFooter>
          <Button
            variant="outline"
            disabled={busy}
            onClick={() => setConfirm(null)}
          >
            Keep reviewing
          </Button>
          <Button
            variant={
              confirm === "delete" || confirm === "cancel"
                ? "destructive"
                : "default"
            }
            disabled={busy}
            onClick={confirmed}
          >
            {busy ? "Working…" : "Confirm"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Preflight({ data }: { data: Checklist }) {
  const c = data.checks;
  return (
    <div className="space-y-4 rounded-md border p-4">
      <h3 className="font-medium">
        {c.ready ? "Ready to send" : "Preflight needs attention"}
      </h3>
      <p className="text-sm text-muted-foreground">
        This read-only estimate uses sample merge values. Audience and available
        quota can change before sending.
      </p>
      <dl className="grid grid-cols-2 gap-4 text-sm md:grid-cols-4">
        {[
          ["Recipients", number(data.recipients)],
          ["Message size", bytes(data.message_bytes)],
          ["Upload", bytes(data.upload_bytes)],
          ["Duration", `${Math.ceil(data.duration_seconds / 60)} minutes`],
          ["Suppressed", number(c.suppressed)],
          ["Inactive", number(c.inactive)],
          ["Skipped", number(c.skipped)],
          ["Daily quota remaining", number(c.remaining_daily_quota)],
          ...(c.user_remaining_daily_quota !== undefined
            ? [["Your daily budget", number(c.user_remaining_daily_quota)]]
            : []),
        ].map(([label, value]) => (
          <div key={label}>
            <dt className="text-muted-foreground">{label}</dt>
            <dd className="mt-1 font-medium">{value}</dd>
          </div>
        ))}
      </dl>
      <p className="text-sm">
        HTML: {bytes(c.html_bytes)}.{" "}
        {c.html_clipping
          ? "Above Gmail’s 102 KB clipping threshold."
          : "Below Gmail’s clipping threshold."}{" "}
        Daily quota: {data.fits_daily_quota ? "fits" : "does not fit"}.
      </p>
      {c.errors?.length > 0 && (
        <ul className="list-disc pl-5 text-sm text-destructive">
          {c.errors.map((i, n) => (
            <li key={n}>
              {i.message}
              {i.tag ? ` (${i.tag})` : ""}
            </li>
          ))}
        </ul>
      )}
      {c.warnings?.length > 0 && (
        <ul className="list-disc pl-5 text-sm">
          {c.warnings.map((i, n) => (
            <li key={n}>
              Warning: {i.message}
              {i.tag ? ` (${i.tag})` : ""}
            </li>
          ))}
        </ul>
      )}
      <details>
        <summary className="cursor-pointer text-sm">
          Link checks ({c.links?.length ?? 0})
        </summary>
        <div className="overflow-auto">
          <table className={tableClass}>
            <thead>
              <tr>
                <th>URL</th>
                <th>Status</th>
                <th>Details</th>
              </tr>
            </thead>
            <tbody>
              {c.links?.map((l, i) => (
                <tr key={i}>
                  <td className="break-all">{l.url}</td>
                  <td>
                    {l.status}
                    {l.http_status ? ` (${l.http_status})` : ""}
                  </td>
                  <td>
                    {l.message}
                    {l.personalized ? " Personalized URL" : ""}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </div>
  );
}
