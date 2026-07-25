// Slice 482 — vitest unit coverage for the pure confidence-band helpers.
//
// classifyBand mirrors the Go thresholds in
// internal/api/ucfcoverage/rollup.go. The boundary cases (0.5, 0.8) are
// the load-bearing assertions: a drift between the TS and Go cut points
// would show one band on the per-row label and a different band on the
// requirement rollup.

import { describe, expect, it } from "vitest";

import { bandStyle, classifyBand } from "./confidence-band";

describe("classifyBand", () => {
  it("maps null / undefined / NaN to uncovered", () => {
    expect(classifyBand(null)).toBe("uncovered");
    expect(classifyBand(undefined)).toBe("uncovered");
    expect(classifyBand(Number.NaN)).toBe("uncovered");
  });

  it("classifies the weak band below 0.5", () => {
    expect(classifyBand(0)).toBe("weak");
    expect(classifyBand(0.49)).toBe("weak");
  });

  it("classifies the partial band in [0.5, 0.8)", () => {
    expect(classifyBand(0.5)).toBe("partial");
    expect(classifyBand(0.7)).toBe("partial");
    expect(classifyBand(0.79)).toBe("partial");
  });

  it("classifies the strong band in [0.8, 1.0] (canvas example floor)", () => {
    expect(classifyBand(0.8)).toBe("strong");
    expect(classifyBand(1)).toBe("strong");
  });

  it("clamps out-of-range values before classifying", () => {
    expect(classifyBand(1.5)).toBe("strong");
    expect(classifyBand(-0.2)).toBe("weak");
  });
});

describe("bandStyle", () => {
  it("returns a distinct style for every band", () => {
    const bands = ["uncovered", "weak", "partial", "strong"] as const;
    const labels = bands.map((b) => bandStyle(b).label);
    // Every band has a non-empty badge class and a human label.
    for (const b of bands) {
      expect(bandStyle(b).badge.length).toBeGreaterThan(0);
      expect(bandStyle(b).label.length).toBeGreaterThan(0);
    }
    // Labels are distinct (no two bands share a gloss).
    expect(new Set(labels).size).toBe(bands.length);
  });
});
