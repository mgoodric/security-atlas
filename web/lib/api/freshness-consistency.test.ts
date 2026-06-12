// Slice 677 / ATLAS-020 — the single-source-of-truth freshness % helper.
//
// These tests pin the AC-2 consistency contract: the dashboard widget and
// the metrics-view board KPI both derive their headline freshness number
// from `freshnessPctFromReport`, so a given FreshnessReport produces ONE
// value on both surfaces. The dashboard subtitle's `computeFreshnessPct`
// delegates to this function (asserted in dashboard-header-subtitle.test);
// the metrics BoardMetricCard calls it directly. One function -> the two
// surfaces cannot disagree.

import { describe, expect, test } from "vitest";

import { computeFreshnessPct } from "@/components/dashboard/dashboard-header-subtitle";

import { freshnessPctFromReport } from "./freshness-consistency";

describe("freshnessPctFromReport", () => {
  test("all fresh -> 100", () => {
    expect(freshnessPctFromReport({ total: 50, total_stale: 0 })).toBe(100);
  });

  test("all stale -> 0 (NOT null, NOT 100)", () => {
    expect(freshnessPctFromReport({ total: 50, total_stale: 50 })).toBe(0);
  });

  test("half stale -> 50", () => {
    expect(freshnessPctFromReport({ total: 50, total_stale: 25 })).toBe(50);
  });

  test("rounds to nearest integer", () => {
    // 1 of 3 stale -> 66.67% fresh -> 67
    expect(freshnessPctFromReport({ total: 3, total_stale: 1 })).toBe(67);
  });

  test("no freshness rows yet (total 0) -> null", () => {
    expect(freshnessPctFromReport({ total: 0, total_stale: 0 })).toBeNull();
  });

  test("undefined / null report -> null", () => {
    expect(freshnessPctFromReport(undefined)).toBeNull();
    expect(freshnessPctFromReport(null)).toBeNull();
  });

  test("defensive: negative total -> null", () => {
    expect(freshnessPctFromReport({ total: -5, total_stale: 0 })).toBeNull();
  });

  test("defensive: total_stale > total clamps to 0%", () => {
    expect(freshnessPctFromReport({ total: 10, total_stale: 99 })).toBe(0);
  });

  test("defensive: missing total_stale treated as 0 stale -> 100", () => {
    // total_stale absent (nullish) -> the `?? 0` path -> all fresh.
    expect(
      freshnessPctFromReport({
        total: 10,
      } as { total: number; total_stale: number }),
    ).toBe(100);
  });
});

describe("freshness consistency across surfaces (AC-2)", () => {
  // The dashboard subtitle delegates to the same definition, so for any
  // (total, total_stale) the dashboard widget and the metrics card agree.
  const cases: Array<[number, number]> = [
    [50, 0],
    [50, 50],
    [50, 25],
    [200, 13],
    [0, 0],
    [3, 1],
  ];

  test.each(cases)(
    "dashboard computeFreshnessPct(%i, %i) === freshnessPctFromReport",
    (total, stale) => {
      expect(computeFreshnessPct(total, stale)).toBe(
        freshnessPctFromReport({ total, total_stale: stale }),
      );
    },
  );
});
