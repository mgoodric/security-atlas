// Slice 664 — vitest unit coverage for the vendor on-time-rate formatter.
//
// Pure helper, no jsdom needed. Pins the zero-vendor empty state (the bug
// this slice fixes) and the unchanged populated-tenant rounding behaviour
// (the anti-criterion: no regression for >= 1 vendor).

import { describe, expect, it } from "vitest";

import { EMPTY_RATE, formatOnTimeRate } from "./vendor-burndown-format";

describe("formatOnTimeRate", () => {
  it("renders the empty token (not 100%) when the population is zero", () => {
    // Backend short-circuits the 0/0 on-time fraction to 1.0; the formatter
    // must guard on the population size, not the fraction value.
    expect(formatOnTimeRate(0, 1.0)).toBe(EMPTY_RATE);
    expect(formatOnTimeRate(0, 1.0)).not.toBe("100%");
  });

  it("renders the empty token for a negative or non-finite population", () => {
    expect(formatOnTimeRate(-3, 0.5)).toBe(EMPTY_RATE);
    expect(formatOnTimeRate(Number.NaN, 0.5)).toBe(EMPTY_RATE);
  });

  it("renders a genuine 100% for a populated, fully on-time tenant", () => {
    // The guard must NOT blank a real 100%-on-time populated tenant.
    expect(formatOnTimeRate(12, 1.0)).toBe("100%");
  });

  it("rounds the fraction to an integer percent for populated tenants", () => {
    expect(formatOnTimeRate(8, 0.75)).toBe("75%");
    expect(formatOnTimeRate(3, 0)).toBe("0%");
    expect(formatOnTimeRate(7, 0.666)).toBe("67%");
    expect(formatOnTimeRate(7, 0.114)).toBe("11%");
  });

  it("exposes the empty token as an em-dash", () => {
    expect(EMPTY_RATE).toBe("—");
  });
});
