import { Send } from "lucide-react";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Textarea } from "../components/ui/textarea";
import { StatusMessage } from "../components/StatusMessage";
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
import { ErrorMessage, number } from "./campaign-shared";
import type { Checklist } from "./campaign-checklist";
import { Preflight } from "./campaign-preflight";

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
  testSendError: unknown;
  testSendStatus: "idle" | "sending" | "sent";
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
  testSendError,
  testSendStatus,
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
        <div
          className="border-t pt-4"
          role="group"
          aria-label="Test email delivery"
        >
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
          <div id="campaign-test-send-feedback" className="mb-2 space-y-2">
            <ErrorMessage error={testSendError} presentation="alert" />
            <StatusMessage
              variant={testSendStatus === "sending" ? "info" : "success"}
            >
              {testSendStatus === "sending"
                ? "Sending test emails…"
                : testSendStatus === "sent"
                  ? "[Test] emails sent."
                  : ""}
            </StatusMessage>
          </div>
          <Button
            variant="info"
            aria-describedby={testSendError ? "campaign-test-send-feedback" : undefined}
            disabled={busy || dirty || !canSend || !testEmails.trim()}
            onClick={() =>
              action("test-send", {
                emails: testEmails.split(/[\s,;]+/).filter(Boolean),
              })
            }
          >
            <Send size={16} />
            {testSendStatus === "sending"
              ? "Sending [Test] emails…"
              : "Send [Test] emails"}
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
