import { Badge } from "./badge";

const tones = {
  active: "success", subscribed: "success", sent: "success", delivered: "success",
  completed: "success", finished: "success", verified: "success", enabled: "success",
  pending: "warning", queued: "warning", scheduled: "warning", paused: "warning",
  unknown: "warning", unconfirmed: "warning",
  failed: "destructive", cancelled: "destructive", canceled: "destructive",
  bounced: "destructive", complained: "destructive", unsubscribed: "destructive", blocklisted: "destructive",
  sending: "info", preparing: "info", claimed: "info", processing: "info",
} satisfies Record<string, NonNullable<Parameters<typeof Badge>[0]["variant"]>>;

export function StatusBadge({ status }: { status: string }) {
  const tone = Object.hasOwn(tones, status) ? tones[status as keyof typeof tones] : "secondary";
  return (
    <Badge variant={tone} className="status-badge">
      <span className="status-dot" aria-hidden="true" />
      {status.charAt(0).toUpperCase() + status.slice(1)}
    </Badge>
  );
}
