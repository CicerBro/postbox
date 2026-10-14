export type CampaignCounters = Partial<Record<
  "recipients" | "sent" | "failed" | "skipped" | "unknown", number
>>;

export function campaignProgress(stats: CampaignCounters | null | undefined) {
  const keys = ["recipients", "sent", "failed", "skipped", "unknown"] as const;
  if (!stats || keys.some(key => !Number.isSafeInteger(stats[key]) || stats[key]! < 0))
    return null;
  const { recipients, sent, failed, skipped, unknown } = stats as Record<typeof keys[number], number>;
  const processed = sent + failed + skipped + unknown;
  return {
    recipients, sent, failed, skipped, unknown, processed,
    pending: Math.max(0, recipients - processed),
    percent: recipients > 0 ? Math.min(100, processed / recipients * 100) : 0,
  };
}

export type ProgressSample = { at: number; processed: number; sent: number };

export function sampleProgress(samples: ProgressSample[], next: ProgressSample) {
  const last = samples.at(-1);
  // Pausing, resuming, re-preparing, or clock changes start a new estimate.
  if (!last || next.at < last.at || next.processed < last.processed || next.sent < last.sent || next.at - last.at > 30000)
    return [next];
  if (next.at === last.at) return samples;
  const recent = samples.filter(sample => sample.at >= next.at - 30000);
  return [...recent, next];
}

export function progressRate(samples: ProgressSample[]) {
  const first = samples[0], last = samples.at(-1);
  if (!first || !last || last.at - first.at < 1000) return null;
  const seconds = (last.at - first.at) / 1000;
  return {
    processedPerSecond: (last.processed - first.processed) / seconds,
    sentPerSecond: (last.sent - first.sent) / seconds,
  };
}

export function remainingTime(seconds: number) {
  const rounded = Math.ceil(seconds);
  if (rounded < 60) return `${rounded}s`;
  if (rounded < 3600) return `${Math.floor(rounded / 60)}m ${rounded % 60}s`;
  const minutes = Math.ceil(rounded / 60);
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}
