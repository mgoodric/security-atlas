// Slice 255 — vitest unit coverage for relative-time formatter (AC-5).
//
// Pure helper, no jsdom needed. The `now` test-seam lets every case run
// against a deterministic clock (no flaky asserts at the minute boundary).

import { describe, expect, it } from "vitest";

import { relativeTime, relativeTimeOrNever } from "./relative-time";

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
