import { useEffect, useState } from "react";
import { campaignProgress, progressRate, remainingTime, sampleProgress, type CampaignCounters, type ProgressSample } from "../lib/campaign-progress";
import { number } from "../pages/campaign-shared";

type Props = {
  stats: CampaignCounters | null | undefined;
  status: string;
  observedAt: number;
  compact?: boolean;
  fresh?: boolean;
};

export function CampaignProgress({ stats, status, observedAt, compact = false, fresh = true }: Props) {
  const progress = campaignProgress(stats);
  const processed = progress?.processed, sent = progress?.sent;
  const [samples, setSamples] = useState<ProgressSample[]>([]);
  useEffect(() => {
    if (status !== "sending" || !fresh || processed === undefined || sent === undefined) {
      setSamples([]);
      return;
    }
    setSamples(current => sampleProgress(current, { at: observedAt, processed, sent }));
  }, [observedAt, processed, sent, status, fresh]);
  const rate = status === "sending" && fresh ? progressRate(samples) : null;
  const eta = rate && progress && rate.processedPerSecond > 0 && progress.pending > 0
    ? remainingTime(progress.pending / rate.processedPerSecond) : null;
  const preparing = status === "preparing";
  const estimate = status === "paused" ? "Sending paused"
    : !fresh ? "Waiting for fresh updates"
    : preparing ? "Preparing audience…"
    : progress?.pending === 0 ? "Finishing send…"
    : eta ? `~${eta} left` : "Estimating time remaining…";

  if (!progress || preparing) return (
    <div className="campaign-progress">
      <p className="text-sm">{preparing ? "Preparing audience…" : "Delivery counters unavailable"}</p>
      <p className="row-subtitle mt-1">{preparing ? "Progress appears once the audience is ready." : "Waiting for delivery counters."}</p>
    </div>
  );

  return (
    <div className={`campaign-progress ${compact ? "campaign-progress-compact" : ""}`}>
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          {!compact && <p className="text-3xl font-semibold tabular-nums">{Math.floor(progress.percent)}%</p>}
          <p className={compact ? "text-sm font-medium tabular-nums" : "row-subtitle mt-1 tabular-nums"}>
            {compact ? `${number(progress.pending)} remaining` : `${number(progress.processed)} / ${number(progress.recipients)} processed`}
          </p>
        </div>
        {compact ? <span className="row-subtitle tabular-nums">{Math.floor(progress.percent)}%</span> : (
          <div className="text-sm"><p>{estimate}</p><p className="row-subtitle tabular-nums">{rate ? `${rate.sentPerSecond.toFixed(1)} emails / second` : ""}</p></div>
        )}
      </div>
      <div className="campaign-progress-track" role="progressbar" aria-label="Recipients processed" aria-valuenow={Math.floor(progress.percent)} aria-valuemin={0} aria-valuemax={100}>
        <div style={{ width: `${progress.percent}%` }} />
      </div>
      {!compact && (
        <dl className="campaign-progress-counters">
          {([ ["Sent", progress.sent], ["Pending", progress.pending], ["Failed", progress.failed], ["Skipped", progress.skipped], ...(progress.unknown ? [["Unknown", progress.unknown] as const] : []) ] as const).map(([label, value]) => (
            <div key={label}><dt className="row-subtitle">{label}</dt><dd className={`mt-1 text-xl font-semibold tabular-nums ${label === "Failed" && value > 0 ? "text-destructive" : ""}`}>{number(value)}</dd></div>
          ))}
        </dl>
      )}
      {!compact && progress.unknown > 0 && <p className="row-subtitle mt-3">Unknown outcomes are processed recipients whose send result could not be confirmed.</p>}
    </div>
  );
}
