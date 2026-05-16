// Slice 102 — vitest unit coverage for /audits filter logic.
//
// Pure-data tests. No React, no DOM, no fetch. Just the row-narrowing
// + filter-set helpers from `./filters`.
//
// All fixtures use neutral identifiers — NO vendor token prefixes,
// NO actor strings that look like real auditor IDs (P0-A5).

import { describe, expect, test } from "vitest";

import type { AuditPeriod } from "@/lib/api";

import {
  ALL,
  applyFilters,
  clearFilters,
  DEFAULT_FILTERS,
  isDefault,
  setFilter,
  uniqueYears,
  yearOf,
} from "./filters";

function period(
  suffix: string,
  overrides: Partial<AuditPeriod> = {},
): AuditPeriod {
  return {
    id: `00000000-0000-0000-0000-0000000000${suffix}`,
    name: `test period ${suffix}`,
    framework_version_id: `00000000-0000-0000-0000-0000000000ff`,
    period_start: "2026-01-01T00:00:00Z",
    period_end: "2026-03-31T23:59:59Z",
    status: "open",
    created_by: "test-actor",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

describe("DEFAULT_FILTERS / isDefault / clearFilters", () => {
  test("DEFAULT_FILTERS is all-ALL", () => {
    expect(DEFAULT_FILTERS.framework).toBe(ALL);
    expect(DEFAULT_FILTERS.status).toBe(ALL);
    expect(DEFAULT_FILTERS.year).toBe(ALL);
  });

  test("isDefault is true for the default set", () => {
    expect(isDefault(DEFAULT_FILTERS)).toBe(true);
  });

  test("isDefault is false when any filter is narrowed", () => {
    expect(isDefault({ ...DEFAULT_FILTERS, status: "frozen" })).toBe(false);
    expect(isDefault({ ...DEFAULT_FILTERS, year: "2026" })).toBe(false);
    expect(isDefault({ ...DEFAULT_FILTERS, framework: "soc2" })).toBe(false);
  });

  test("clearFilters returns a fresh ALL set (no shared reference)", () => {
    const cleared = clearFilters();
    expect(cleared).toEqual(DEFAULT_FILTERS);
    expect(cleared).not.toBe(DEFAULT_FILTERS);
  });
});

describe("setFilter", () => {
  test("merges one key without mutating the input", () => {
    const input = clearFilters();
    const next = setFilter(input, "status", "frozen");
    expect(next.status).toBe("frozen");
    expect(input.status).toBe(ALL); // unchanged
  });

  test("multiple sets compose left-to-right", () => {
    const next = setFilter(
      setFilter(clearFilters(), "status", "frozen"),
      "year",
      "2026",
    );
    expect(next.status).toBe("frozen");
    expect(next.year).toBe("2026");
  });
});

describe("yearOf / uniqueYears", () => {
  test("yearOf returns the YYYY prefix of period_start", () => {
    expect(yearOf(period("01", { period_start: "2026-04-01T00:00:00Z" }))).toBe(
      "2026",
    );
    expect(yearOf(period("02", { period_start: "2024-12-31T00:00:00Z" }))).toBe(
      "2024",
    );
  });

  test("uniqueYears returns the year set sorted descending (newest first)", () => {
    const rows: AuditPeriod[] = [
      period("01", { period_start: "2025-01-01T00:00:00Z" }),
      period("02", { period_start: "2026-04-01T00:00:00Z" }),
      period("03", { period_start: "2025-07-01T00:00:00Z" }),
      period("04", { period_start: "2024-10-01T00:00:00Z" }),
    ];
    expect(uniqueYears(rows)).toEqual(["2026", "2025", "2024"]);
  });

  test("uniqueYears returns empty array for empty input", () => {
    expect(uniqueYears([])).toEqual([]);
  });
});

describe("applyFilters", () => {
  const allRows: AuditPeriod[] = [
    period("01", {
      status: "open",
      period_start: "2026-04-01T00:00:00Z",
      period_end: "2026-06-30T00:00:00Z",
    }),
    period("02", {
      status: "frozen",
      period_start: "2026-01-01T00:00:00Z",
      period_end: "2026-03-31T00:00:00Z",
    }),
    period("03", {
      status: "frozen",
      period_start: "2025-10-01T00:00:00Z",
      period_end: "2025-12-31T00:00:00Z",
    }),
    period("04", {
      status: "open",
      period_start: "2025-06-01T00:00:00Z",
      period_end: "2025-08-31T00:00:00Z",
    }),
  ];

  test("DEFAULT_FILTERS returns every row", () => {
    expect(applyFilters(allRows, DEFAULT_FILTERS)).toHaveLength(4);
  });

  test("status filter narrows on exact status match", () => {
    const out = applyFilters(allRows, {
      ...DEFAULT_FILTERS,
      status: "frozen",
    });
    expect(out).toHaveLength(2);
    expect(out.every((p) => p.status === "frozen")).toBe(true);
  });

  test("status filter returns empty when no row matches", () => {
    const out = applyFilters(allRows, {
      ...DEFAULT_FILTERS,
      status: "closed",
    });
    expect(out).toEqual([]);
  });

  test("year filter narrows on YYYY prefix of period_start", () => {
    const out = applyFilters(allRows, { ...DEFAULT_FILTERS, year: "2026" });
    expect(out).toHaveLength(2);
    expect(out.every((p) => p.period_start.startsWith("2026"))).toBe(true);
  });

  test("year filter returns empty for years with no periods", () => {
    const out = applyFilters(allRows, { ...DEFAULT_FILTERS, year: "2020" });
    expect(out).toEqual([]);
  });

  test("framework filter is a no-op for v1 (no label endpoint)", () => {
    // Even with framework narrowed, every row stays — until the label
    // endpoint lands. The pill still renders so the UI shape stays
    // stable across slices.
    const out = applyFilters(allRows, {
      ...DEFAULT_FILTERS,
      framework: "soc2",
    });
    expect(out).toHaveLength(4);
  });

  test("combining status + year narrows on every active predicate", () => {
    const out = applyFilters(allRows, {
      ...DEFAULT_FILTERS,
      status: "frozen",
      year: "2026",
    });
    expect(out).toHaveLength(1);
    expect(out[0]?.status).toBe("frozen");
    expect(out[0]?.period_start.startsWith("2026")).toBe(true);
  });
});
