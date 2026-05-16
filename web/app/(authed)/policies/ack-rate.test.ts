// Slice 101 — vitest coverage for ack-rate formatter + color bands.
//
// Per AC-9 the unit tests cover:
//   * formatAckRate "98% · 142/145" caption shape
//   * ackRateBand thresholds: green >=95, amber 70-94, red <70
//   * ackRateColor returns the right Tailwind class per band
//   * null / undefined / NaN inputs all return em-dash + `none` band
//
// All test data is neutral (no vendor tokens). The thresholds asserted
// here mirror the slice 101 decisions log (SOC 2 CC1.4 default).

import { describe, expect, test } from "vitest";

import {
  ackRateAriaLabel,
  ackRateBand,
  ackRateColor,
  ackRateTextColor,
  formatAckRate,
  type AckRateBand,
} from "./ack-rate";

describe("ackRateBand", () => {
  test("100% bands green", () => {
    expect(ackRateBand(100)).toBe("green");
  });
  test("95% is the lower edge of green", () => {
    expect(ackRateBand(95)).toBe("green");
  });
  test("94.9% bands amber (just below the green floor)", () => {
    expect(ackRateBand(94.9)).toBe("amber");
  });
  test("70% is the lower edge of amber", () => {
    expect(ackRateBand(70)).toBe("amber");
  });
  test("69.9% bands red", () => {
    expect(ackRateBand(69.9)).toBe("red");
  });
  test("0% bands red", () => {
    expect(ackRateBand(0)).toBe("red");
  });
  test("null bands none", () => {
    expect(ackRateBand(null)).toBe("none");
  });
  test("NaN bands none", () => {
    expect(ackRateBand(Number.NaN)).toBe("none");
  });
});

describe("ackRateColor", () => {
  const cases: { band: AckRateBand; want: string }[] = [
    { band: "green", want: "bg-emerald-500" },
    { band: "amber", want: "bg-amber-500" },
    { band: "red", want: "bg-rose-500" },
    { band: "none", want: "bg-muted-foreground/30" },
  ];
  for (const c of cases) {
    test(`band ${c.band} maps to ${c.want}`, () => {
      expect(ackRateColor(c.band)).toBe(c.want);
    });
  }
});

describe("ackRateTextColor", () => {
  test("red band gets rose-700 (matches mockup line 271)", () => {
    expect(ackRateTextColor("red")).toBe("text-rose-700");
  });
  test("green band gets foreground", () => {
    expect(ackRateTextColor("green")).toBe("text-foreground");
  });
  test("amber band gets foreground", () => {
    expect(ackRateTextColor("amber")).toBe("text-foreground");
  });
  test("none band gets muted-foreground", () => {
    expect(ackRateTextColor("none")).toBe("text-muted-foreground");
  });
});

describe("formatAckRate", () => {
  test("renders 98% · 142/145 caption (mockup shape)", () => {
    expect(
      formatAckRate({ numerator: 142, denominator: 145, percent: 97.93 }),
    ).toBe("98% · 142/145");
  });
  test("rounds half-up at the integer boundary", () => {
    expect(
      formatAckRate({ numerator: 73, denominator: 100, percent: 72.5 }),
    ).toBe("73% · 73/100");
  });
  test("null rate renders em-dash", () => {
    expect(formatAckRate(null)).toBe("—");
  });
  test("undefined rate renders em-dash", () => {
    expect(formatAckRate(undefined)).toBe("—");
  });
  test("null percent renders em-dash (denominator-zero case)", () => {
    expect(formatAckRate({ numerator: 0, denominator: 0, percent: null })).toBe(
      "—",
    );
  });
  test("NaN percent renders em-dash", () => {
    expect(
      formatAckRate({
        numerator: 0,
        denominator: 1,
        percent: Number.NaN,
      }),
    ).toBe("—");
  });
});

describe("ackRateAriaLabel", () => {
  test("real rate reads as sentence (142 of 145 acknowledged · 98%)", () => {
    expect(
      ackRateAriaLabel({ numerator: 142, denominator: 145, percent: 97.93 }),
    ).toBe("142 of 145 acknowledged · 98%");
  });
  test("null rate reads 'Acknowledgment rate not available'", () => {
    expect(ackRateAriaLabel(null)).toBe("Acknowledgment rate not available");
  });
  test("null percent reads 'Acknowledgment rate not available'", () => {
    expect(
      ackRateAriaLabel({ numerator: 0, denominator: 0, percent: null }),
    ).toBe("Acknowledgment rate not available");
  });
});
