// Slice 229 — vitest coverage for the pure helpers that power the
// dashboard header tenant/freshness subtitle.
//
// The component itself (`DashboardHeaderSubtitle`) integrates TanStack
// Query + Next.js client boundary and is hostile to a vitest unit test;
// that surface is owned by the Playwright e2e (`web/e2e/dashboard.spec.ts`).
// The pure helpers are unit-testable here.
//
// AC-2: subtitle renders "evidence freshness {pct}% within window" where
// pct = 100 * (1 - total_stale / total) rounded to int.
// AC-5: when total === 0, the subtitle renders "No evidence ingested yet" —
// honest about empty state, not "100% fresh of 0".
//
// Slice 229 trims AC-2 to the freshness pct only; the snapshot timestamp
// ("Snapshot taken N minutes ago") is OMITTED because the backing wire
// shape (FreshnessReport) does not currently expose a snapshot timestamp.
// See `docs/audit-log/229-dashboard-header-subtitle-decisions.md` D1.

import { describe, expect, test } from "vitest";

import {
  computeFreshnessPct,
  formatFreshnessSubtitle,
  formatTenantContext,
} from "./dashboard-header-subtitle";

describe("computeFreshnessPct", () => {
  test("returns null when total is 0 (no evidence ingested yet — AC-5)", () => {
    expect(computeFreshnessPct(0, 0)).toBeNull();
  });

  test("returns null when total is negative (defensive)", () => {
    expect(computeFreshnessPct(-1, 0)).toBeNull();
  });

  test("returns 100 when no stale evidence", () => {
    expect(computeFreshnessPct(10, 0)).toBe(100);
  });

  test("returns 0 when all evidence is stale", () => {
    expect(computeFreshnessPct(10, 10)).toBe(0);
  });

  test("rounds to nearest integer (AC-2)", () => {
    // 87.5 -> 88 (round half to even / nearest int per JS Math.round)
    expect(computeFreshnessPct(8, 1)).toBe(88);
    // 87 / 100 = 0.87 -> 87
    expect(computeFreshnessPct(100, 13)).toBe(87);
    // 66.66... -> 67
    expect(computeFreshnessPct(3, 1)).toBe(67);
  });

  test("clamps total_stale > total to 0 pct (defensive — should never happen)", () => {
    // If the backend ever returns nonsensical counts, do not show a
    // negative pct.
    expect(computeFreshnessPct(5, 10)).toBe(0);
  });
});

describe("formatFreshnessSubtitle", () => {
  test("returns the AC-5 empty-state copy when pct is null (total === 0)", () => {
    expect(formatFreshnessSubtitle(null)).toBe("No evidence ingested yet");
  });

  test("formats the pct as 'evidence freshness {N}% within window' (AC-2)", () => {
    expect(formatFreshnessSubtitle(87)).toBe(
      "evidence freshness 87% within window",
    );
    expect(formatFreshnessSubtitle(100)).toBe(
      "evidence freshness 100% within window",
    );
    expect(formatFreshnessSubtitle(0)).toBe(
      "evidence freshness 0% within window",
    );
  });
});

describe("formatTenantContext", () => {
  test("returns the tenant name as-is when non-empty (AC-1)", () => {
    expect(formatTenantContext("Sentinel Labs")).toBe("Sentinel Labs");
  });

  test("returns null when the name is empty (silent absence)", () => {
    expect(formatTenantContext("")).toBeNull();
  });

  test("returns null when the name is undefined (silent absence)", () => {
    expect(formatTenantContext(undefined)).toBeNull();
  });

  test("trims surrounding whitespace before evaluating empty", () => {
    expect(formatTenantContext("   ")).toBeNull();
    expect(formatTenantContext("  Acme  ")).toBe("Acme");
  });
});
