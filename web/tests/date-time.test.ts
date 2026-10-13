import { describe, expect, test } from "bun:test";
import { createDateTimeFormatter, formatDate, resolveTimeZone, zonedDateTimeToTimestamp } from "../src/lib/date-time";

const calendarOptions: Intl.DateTimeFormatOptions = { year: "numeric", month: "2-digit", day: "2-digit" };

describe("timezone-aware dates", () => {
  test("one instant rolls into different local calendar dates", () => {
    const value = "2026-01-01T00:30:00Z";
    const expected = (timeZone: string) => new Intl.DateTimeFormat(undefined, { ...calendarOptions, timeZone }).format(new Date(value));
    expect(formatDate(value, "America/Los_Angeles", calendarOptions)).toBe(expected("America/Los_Angeles"));
    expect(formatDate(value, "Asia/Tokyo", calendarOptions)).toBe(expected("Asia/Tokyo"));
    expect(formatDate(value, "America/Los_Angeles", calendarOptions)).not.toBe(formatDate(value, "Asia/Tokyo", calendarOptions));
  });

  test("calendar-only dates retain their day across timezones", () => {
    expect(formatDate("2026-01-01", "America/Los_Angeles", calendarOptions)).toBe(formatDate("2026-01-01", "Asia/Tokyo", calendarOptions));
  });

  test("missing, malformed, and epoch timestamps are handled", () => {
    const formatter = createDateTimeFormatter("UTC");
    expect(formatter.formatDateTime(undefined)).toBe("Not recorded");
    expect(formatter.formatDateTime("invalid")).toBe("Not recorded");
    expect(formatter.formatDateTime(0)).not.toBe("Not recorded");
    expect(resolveTimeZone("invalid")).toBe(resolveTimeZone(""));
  });

  test("display honors daylight saving changes", () => {
    const formatter = createDateTimeFormatter("America/New_York");
    const options: Intl.DateTimeFormatOptions = { hour: "2-digit", minute: "2-digit", hourCycle: "h23" };
    expect(formatter.formatTime("2026-03-08T06:30:00Z", options)).toBe("01:30");
    expect(formatter.formatTime("2026-03-08T07:30:00Z", options)).toBe("03:30");
  });
});

describe("campaign schedule wall clock conversion", () => {
  test("uses the selected timezone, including fractional offsets", () => {
    expect(zonedDateTimeToTimestamp("2026-01-01T00:30", "Asia/Kathmandu")).toBe(Date.parse("2025-12-31T18:45:00Z"));
    expect(zonedDateTimeToTimestamp("2026-07-01T12:00", "Europe/London")).toBe(Date.parse("2026-07-01T11:00:00Z"));
  });
  test("rejects nonexistent dates and daylight saving wall times", () => {
    expect(zonedDateTimeToTimestamp("2026-03-08T02:30", "America/New_York")).toBeNaN();
    expect(zonedDateTimeToTimestamp("2026-02-30T12:00", "UTC")).toBeNaN();
    expect(zonedDateTimeToTimestamp("invalid", "UTC")).toBeNaN();
  });
  test("repeated daylight saving times resolve to the earlier occurrence", () => {
    expect(zonedDateTimeToTimestamp("2026-11-01T01:30", "America/New_York")).toBe(Date.parse("2026-11-01T05:30:00Z"));
  });
});
