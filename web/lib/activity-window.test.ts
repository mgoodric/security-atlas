import { describe, expect, it } from "vitest";

import {
  DEFAULT_WINDOW_DAYS,
  defaultActivityWindow,
  toRFC3339Day,
} from "./activity-window";

describe("defaultActivityWindow (#2464)", () => {
  it("ends at now, so an event written moments ago is inside it", () => {
    const now = new Date("2026-11-18T17:45:12.345Z");
    const w = defaultActivityWindow(now);
    expect(w.to).toBe("2026-11-18T17:45:12.345Z");
    const justNow = Date.parse("2026-11-18T17:45:00.000Z");
    expect(justNow).toBeGreaterThanOrEqual(Date.parse(w.from));
    expect(justNow).toBeLessThan(Date.parse(w.to));
  });

  it("starts at 00:00 UTC, DEFAULT_WINDOW_DAYS days back", () => {
    const w = defaultActivityWindow(new Date("2026-11-18T17:45:12.345Z"));
    expect(DEFAULT_WINDOW_DAYS).toBe(7);
    expect(w.from).toBe("2026-11-11T00:00:00.000Z");
  });

  it("uses the UTC day just after UTC midnight", () => {
    // 00:30 UTC on the 18th is the evening of the 17th in Denver.
    const w = defaultActivityWindow(new Date("2026-11-18T00:30:00.000Z"));
    expect(w.from).toBe("2026-11-11T00:00:00.000Z");
    expect(w.to).toBe("2026-11-18T00:30:00.000Z");
  });

  it("is a valid window: to after from, well under the 90-day bound", () => {
    const w = defaultActivityWindow(new Date("2026-03-01T12:00:00.000Z"));
    const span = Date.parse(w.to) - Date.parse(w.from);
    expect(span).toBeGreaterThan(0);
    expect(span).toBeLessThanOrEqual(90 * 24 * 60 * 60 * 1000);
  });
});

describe("toRFC3339Day", () => {
  it("truncates to 00:00 UTC of the UTC day", () => {
    expect(toRFC3339Day(new Date("2026-11-18T23:59:59.999Z"))).toBe(
      "2026-11-18T00:00:00.000Z",
    );
  });
});
