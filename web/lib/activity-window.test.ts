import { afterAll, beforeAll, describe, expect, it } from "vitest";

import {
  DEFAULT_WINDOW_DAYS,
  defaultActivityWindow,
  fromBoundForDay,
  fromInputValue,
  toBoundForDay,
  toInputValue,
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

// #2658 — the date inputs read and write the viewer's local day. Node
// honours a runtime TZ change, so these run west of UTC.
describe("local-day date inputs (#2658, America/Denver)", () => {
  let savedTZ: string | undefined;
  beforeAll(() => {
    savedTZ = process.env.TZ;
    process.env.TZ = "America/Denver";
  });
  afterAll(() => {
    if (savedTZ === undefined) delete process.env.TZ;
    else process.env.TZ = savedTZ;
  });

  it("a picked From day starts at local midnight", () => {
    // MST is UTC-7 in November.
    expect(fromBoundForDay("2026-11-18")).toBe("2026-11-18T07:00:00.000Z");
  });

  it("a picked To day is inclusive: the bound is the next local midnight", () => {
    const to = toBoundForDay("2026-11-18");
    expect(to).toBe("2026-11-19T07:00:00.000Z");
    // An event written on a Denver evening of that day is inside it.
    const evening = Date.parse("2026-11-19T04:30:00.000Z"); // 21:30 MST
    expect(evening).toBeLessThan(Date.parse(to ?? ""));
  });

  it("follows the DST change: the bound is midnight, not 24h later", () => {
    // 2026-11-01 is the fall-back day (25 hours long) in Denver.
    expect(fromBoundForDay("2026-11-01")).toBe("2026-11-01T06:00:00.000Z");
    expect(toBoundForDay("2026-11-01")).toBe("2026-11-02T07:00:00.000Z");
  });

  it("rejects a cleared or malformed day", () => {
    expect(fromBoundForDay("")).toBeNull();
    expect(toBoundForDay("not-a-day")).toBeNull();
  });

  it("shows a picked day as itself", () => {
    expect(toInputValue(toBoundForDay("2026-11-18") ?? "")).toBe("2026-11-18");
    expect(fromInputValue(fromBoundForDay("2026-11-18") ?? "")).toBe(
      "2026-11-18",
    );
  });

  it("shows the default To (now) as the viewer's today, not the UTC date", () => {
    // 20:00 on the 18th in Denver is already the 19th in UTC.
    const w = defaultActivityWindow(new Date("2026-11-19T03:00:00.000Z"));
    expect(w.to.slice(0, 10)).toBe("2026-11-19");
    expect(toInputValue(w.to)).toBe("2026-11-18");
  });

  it("returns an empty value for an unparsable bound", () => {
    expect(fromInputValue("garbage")).toBe("");
    expect(toInputValue("")).toBe("");
  });
});
