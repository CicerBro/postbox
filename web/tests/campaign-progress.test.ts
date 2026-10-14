import { describe, expect, test } from "bun:test";
import { campaignProgress, progressRate, sampleProgress, remainingTime } from "../src/lib/campaign-progress";

describe("campaign delivery progress", () => {
  test("processed includes terminal outcomes while delivered does not count twice", () => {
    const result = campaignProgress({ recipients: 100, sent: 50, failed: 5, skipped: 3, unknown: 2 });
    expect(result?.processed).toBe(60);
    expect(result?.pending).toBe(40);
    expect(result?.percent).toBe(60);
  });
  test("missing counters stay unavailable, and empty audiences have no invalid percentage", () => {
    expect(campaignProgress(null)).toBeNull();
    expect(campaignProgress({ recipients: 100, sent: 50 })).toBeNull();
    expect(campaignProgress({ recipients: 0, sent: 0, failed: 0, skipped: 0, unknown: 0 })?.percent).toBe(0);
    expect(campaignProgress({ recipients: 100, sent: -1, failed: 0, skipped: 0, unknown: 0 })).toBeNull();
  });
  test("recent throughput estimates finish time using processed outcomes", () => {
    let samples = sampleProgress([], { at: 1000, processed: 60, sent: 50 });
    expect(progressRate(samples)).toBeNull();
    samples = sampleProgress(samples, { at: 6000, processed: 80, sent: 65 });
    expect(progressRate(samples)).toEqual({ processedPerSecond: 4, sentPerSecond: 3 });
    expect(remainingTime(40 / progressRate(samples)!.processedPerSecond)).toBe("10s");
    expect(remainingTime(7199)).toBe("2h 0m");
    expect(sampleProgress(samples, { at: 6000, processed: 80, sent: 65 })).toBe(samples);
  });
  test("old, regressed, or interrupted samples do not produce misleading estimates", () => {
    const samples = [{ at: 1000, processed: 60, sent: 50 }, { at: 6000, processed: 80, sent: 65 }];
    expect(progressRate(sampleProgress(samples, { at: 40000, processed: 90, sent: 70 }))).toBeNull();
    expect(progressRate(sampleProgress(samples, { at: 7000, processed: 0, sent: 0 }))).toBeNull();
    expect(progressRate(sampleProgress(samples, { at: 500, processed: 85, sent: 70 }))).toBeNull();
    let stalled = samples;
    for (let at = 11000; at <= 41000; at += 5000) stalled = sampleProgress(stalled, { at, processed: 80, sent: 65 });
    expect(progressRate(stalled)).toEqual({ processedPerSecond: 0, sentPerSecond: 0 });
  });
});
