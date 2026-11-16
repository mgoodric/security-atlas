// Slice 255 — vitest unit coverage for relative-time formatter (AC-5).
//
// Pure helper, no jsdom needed. The `now` test-seam lets every case run
// against a deterministic clock (no flaky asserts at the minute boundary).

import { describe, expect, it } from "vitest";

import {
  exactUTC,
  formatTimestamp,
  RECENT_WINDOW_MS,
  relativeTime,
  relativeTimeOrNever,
} from "./relative-time";

// Fixed clock for every test: 2026-05-24T12:00:00.000Z.
const NOW = Date.UTC(2026, 4, 24, 12, 0, 0);

describe("relativeTime", () => {
  it("renders '8 minutes ago' for a timestamp 8 minutes in the past (AC-5 mockup parity)", () => {
    const eightAgo = new Date(NOW - 8 * 60_000).toISOString();
    expect(relativeTime(eightAgo, NOW)).toBe("8 minutes ago");
  });

  it("uses singular form for exactly one minute", () => {
    const oneAgo = new Date(NOW - 1 * 60_000).toISOString();
    expect(relativeTime(oneAgo, NOW)).toBe("1 minute ago");
  });

  it("renders 'just now' for sub-minute deltas", () => {
    const justNow = new Date(NOW - 5_000).toISOString();
    expect(relativeTime(justNow, NOW)).toBe("just now");
  });

  it("clamps future timestamps to 'just now' (clock-skew defense)", () => {
    const future = new Date(NOW + 30_000).toISOString();
    expect(relativeTime(future, NOW)).toBe("just now");
  });

  it("escalates to hours past the 60-minute boundary", () => {
    const ninetyMin = new Date(NOW - 90 * 60_000).toISOString();
    expect(relativeTime(ninetyMin, NOW)).toBe("1 hour ago");
    const threeHours = new Date(NOW - 3 * 60 * 60_000).toISOString();
    expect(relativeTime(threeHours, NOW)).toBe("3 hours ago");
  });

  it("escalates to days past the 24-hour boundary (AC-5 mockup '1 day ago')", () => {
    const oneDay = new Date(NOW - 24 * 60 * 60_000).toISOString();
    expect(relativeTime(oneDay, NOW)).toBe("1 day ago");
    const fiveDays = new Date(NOW - 5 * 24 * 60 * 60_000).toISOString();
    expect(relativeTime(fiveDays, NOW)).toBe("5 days ago");
  });

  it("renders '—' for null, undefined, and unparsable input (AC-5)", () => {
    expect(relativeTime(null, NOW)).toBe("—");
    expect(relativeTime(undefined, NOW)).toBe("—");
    expect(relativeTime("not-a-date", NOW)).toBe("—");
    expect(relativeTime("", NOW)).toBe("—");
  });
});

describe("relativeTimeOrNever", () => {
  it("renders 'never' for explicit null (state exists but no evidence yet)", () => {
    expect(relativeTimeOrNever(null, NOW)).toBe("never");
  });

  it("renders '—' for undefined (no state record at all)", () => {
    expect(relativeTimeOrNever(undefined, NOW)).toBe("—");
  });

  it("delegates to relativeTime for a real timestamp", () => {
    const eightAgo = new Date(NOW - 8 * 60_000).toISOString();
    expect(relativeTimeOrNever(eightAgo, NOW)).toBe("8 minutes ago");
  });
});

describe("formatTimestamp (#2084)", () => {
  const opts = { now: NOW, locale: "en-GB", timeZone: "UTC" } as const;
  const ago = (ms: number) => new Date(NOW - ms).toISOString();
  const DAY = 24 * 60 * 60 * 1000;

  it("is relative just inside the 7-day window", () => {
    expect(formatTimestamp(ago(RECENT_WINDOW_MS - 60_000), opts).text).toBe(
      "6 days ago",
    );
    expect(formatTimestamp(ago(2 * DAY), opts).text).toBe("2 days ago");
  });

  it("is absolute at exactly 7 days and beyond", () => {
    expect(formatTimestamp(ago(RECENT_WINDOW_MS), opts).text).toBe(
      "17 May 2026, 12:00",
    );
    expect(formatTimestamp(ago(30 * DAY), opts).text).toBe(
      "24 Apr 2026, 12:00",
    );
  });

  it("never renders the date or datetime style as relative", () => {
    expect(formatTimestamp(ago(DAY), { ...opts, style: "date" }).text).toBe(
      "23 May 2026",
    );
    expect(formatTimestamp(ago(DAY), { ...opts, style: "datetime" }).text).toBe(
      "23 May 2026, 12:00",
    );
  });

  it("renders a future stamp beyond a minute as absolute, not 'just now'", () => {
    const soon = new Date(NOW + 30_000).toISOString();
    expect(formatTimestamp(soon, opts).text).toBe("just now");
    const later = new Date(NOW + 3 * DAY).toISOString();
    expect(formatTimestamp(later, opts).text).toBe("27 May 2026, 12:00");
  });

  it("converts instants into the viewer's timezone", () => {
    const iso = "2026-05-24T02:30:00Z";
    const old = { now: Date.UTC(2026, 6, 1), locale: "en-GB" };
    expect(formatTimestamp(iso, { ...old, timeZone: "UTC" }).text).toBe(
      "24 May 2026, 2:30",
    );
    expect(
      formatTimestamp(iso, { ...old, timeZone: "America/New_York" }).text,
    ).toBe("23 May 2026, 22:30");
    expect(formatTimestamp(iso, { ...old, timeZone: "Asia/Tokyo" }).text).toBe(
      "24 May 2026, 11:30",
    );
  });

  it("follows the DST switch in the viewer's timezone", () => {
    // US spring-forward 2026-03-08: 02:00 EST jumps to 03:00 EDT (07:00Z).
    const tz = {
      now: Date.UTC(2026, 6, 1),
      locale: "en-GB",
      timeZone: "America/New_York",
    };
    expect(formatTimestamp("2026-03-08T06:30:00Z", tz).text).toBe(
      "8 Mar 2026, 1:30",
    );
    expect(formatTimestamp("2026-03-08T07:30:00Z", tz).text).toBe(
      "8 Mar 2026, 3:30",
    );
  });

  it("keeps a calendar day on its day whatever the viewer's timezone", () => {
    const west = { ...opts, timeZone: "America/Los_Angeles" };
    expect(formatTimestamp("2026-11-14", west).text).toBe("14 Nov 2026");
    expect(
      formatTimestamp("2026-11-14T00:00:00Z", { ...west, style: "date" }).text,
    ).toBe("14 Nov 2026");
  });

  it("formats in the viewer's locale", () => {
    const iso = "2026-11-14T09:30:00Z";
    const base = { now: NOW, timeZone: "UTC", style: "date" } as const;
    expect(formatTimestamp(iso, { ...base, locale: "en-GB" }).text).toBe(
      "14 Nov 2026",
    );
    expect(formatTimestamp(iso, { ...base, locale: "en-US" }).text).toBe(
      "Nov 14, 2026",
    );
    expect(formatTimestamp(iso, { ...base, locale: "de-DE" }).text).toBe(
      "14. Nov. 2026",
    );
  });

  it("puts the exact UTC instant, labelled, in the tooltip", () => {
    const f = formatTimestamp("2026-05-22T09:30:05.123-04:00", opts);
    expect(f.title).toBe("2026-05-22 13:30:05 UTC");
    expect(f.dateTime).toBe("2026-05-22T09:30:05.123-04:00");
    expect(formatTimestamp("2026-11-14", opts).title).toBe("2026-11-14");
  });

  it("renders null, empty and unparsable input as an em dash", () => {
    for (const v of [null, undefined, "", "not-a-date"]) {
      expect(formatTimestamp(v, opts)).toEqual({
        text: "—",
        title: "",
        dateTime: "",
      });
      expect(exactUTC(v)).toBe("");
    }
  });
});
