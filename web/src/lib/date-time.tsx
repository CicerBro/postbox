import { createContext, useContext, useMemo, type ReactNode } from "react";

export type DateTimeValue = number | string | Date | null | undefined;

export function resolveTimeZone(value?: string): string {
  try {
    return new Intl.DateTimeFormat(undefined, value ? { timeZone: value } : undefined)
      .resolvedOptions().timeZone;
  } catch {
    return new Intl.DateTimeFormat().resolvedOptions().timeZone;
  }
}

function dateValue(value: DateTimeValue): Date | null {
  if (value === null || value === undefined || value === "") return null;
  const date = value instanceof Date ? value : new Date(value);
  return Number.isFinite(date.getTime()) ? date : null;
}

export function formatDateTime(value: DateTimeValue, timeZone: string, options?: Intl.DateTimeFormatOptions): string {
  if (typeof value === "string" && /^\d{4}-\d{2}-\d{2}$/.test(value)) return formatDate(value, timeZone, options);
  const date = dateValue(value);
  if (!date) return "Not recorded";
  return new Intl.DateTimeFormat(undefined, {
    ...(options?.dateStyle || options?.timeStyle ? {} : {
      year: "numeric", month: "short", day: "numeric", hour: "numeric", minute: "2-digit",
    } as const),
    ...options, timeZone: resolveTimeZone(timeZone),
  }).format(date);
}

export function formatDate(value: DateTimeValue, timeZone: string, options?: Intl.DateTimeFormatOptions): string {
  const date = dateValue(value);
  if (!date) return "Not recorded";
  // A calendar date has no instant or timezone and must not move to the prior day.
  const calendarDate = typeof value === "string" && /^\d{4}-\d{2}-\d{2}$/.test(value);
  return new Intl.DateTimeFormat(undefined, {
    ...(options?.dateStyle || options?.timeStyle ? {} : { year: "numeric", month: "short", day: "numeric" } as const),
    ...options,
    timeZone: calendarDate ? "UTC" : resolveTimeZone(timeZone),
  }).format(date);
}

export function formatTime(value: DateTimeValue, timeZone: string, options?: Intl.DateTimeFormatOptions): string {
  const date = dateValue(value);
  if (!date) return "Not recorded";
  return new Intl.DateTimeFormat(undefined, {
    ...(options?.dateStyle || options?.timeStyle ? {} : { hour: "numeric", minute: "2-digit" } as const),
    ...options, timeZone: resolveTimeZone(timeZone),
  }).format(date);
}

export function createDateTimeFormatter(timeZone: string) {
  const resolved = resolveTimeZone(timeZone);
  return {
    timeZone: resolved,
    formatDateTime: (value: DateTimeValue, options?: Intl.DateTimeFormatOptions) => formatDateTime(value, resolved, options),
    formatDate: (value: DateTimeValue, options?: Intl.DateTimeFormatOptions) => formatDate(value, resolved, options),
    formatTime: (value: DateTimeValue, options?: Intl.DateTimeFormatOptions) => formatTime(value, resolved, options),
  };
}

const DateTimeContext = createContext(createDateTimeFormatter(""));

export function TimeZoneProvider({ timeZone, children }: { timeZone: string; children: ReactNode }) {
  const value = useMemo(() => createDateTimeFormatter(timeZone), [timeZone]);
  return <DateTimeContext.Provider value={value}>{children}</DateTimeContext.Provider>;
}

export function useDateTime() {
  return useContext(DateTimeContext);
}

export function DateTime({ value, fallback = "Not recorded" }: { value: DateTimeValue; fallback?: string }) {
  const { formatDateTime } = useDateTime();
  return <>{dateValue(value) ? formatDateTime(value) : fallback}</>;
}

/** Interpret a datetime-local wall clock in the selected IANA timezone.
 * Reject skipped DST times; when clocks repeat, choose the earlier occurrence.
 */
export function zonedDateTimeToTimestamp(value: string, timeZone: string): number {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2}))?$/.exec(value);
  if (!match) return NaN;
  const [, year, month, day, hour, minute, second = "0"] = match;
  const wall = Date.UTC(+year, +month - 1, +day, +hour, +minute, +second);
  const normalized = new Date(wall);
  if (normalized.getUTCFullYear() !== +year || normalized.getUTCMonth() !== +month - 1 ||
      normalized.getUTCDate() !== +day || +hour > 23 || +minute > 59 || +second > 59) return NaN;
  const formatter = new Intl.DateTimeFormat("en-GB", {
    timeZone: resolveTimeZone(timeZone), year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23",
  });
  const localWall = (timestamp: number) => {
    const parts = Object.fromEntries(formatter.formatToParts(timestamp).map(part => [part.type, part.value]));
    return Date.UTC(+parts.year, +parts.month - 1, +parts.day, +parts.hour, +parts.minute, +parts.second);
  };
  const offsets = new Set<number>();
  for (let hours = -48; hours <= 48; hours += 6) {
    const sample = wall + hours * 60 * 60 * 1000;
    offsets.add(localWall(sample) - sample);
  }
  const candidates = [...offsets].map(offset => wall - offset).filter(timestamp => localWall(timestamp) === wall);
  return candidates.length ? Math.min(...candidates) : NaN;
}
