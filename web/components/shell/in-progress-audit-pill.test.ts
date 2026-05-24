// Slice 213 — vitest coverage for the in-progress audit pill helper.
//
// The component itself integrates TanStack Query + Next.js server/
// client boundary and is hostile to a vitest unit test; that surface
// is owned by the Playwright e2e (`web/e2e/audits-header.spec.ts`).
// The pure picker function is unit-testable here.
//
// AC-3: "filters to in-progress status, renders the most-recently-
// started period as an amber pill". v1 DB status for the in-progress
// surface is `'open'` (see the pill source for the rationale). This
// file pins "most-recently-started" + "filter narrows" + "zero match
// returns null" branches.
//
// AC-5 (the "absent otherwise" half): when zero periods are active,
// the helper returns null and the component returns null — silent-
// absence (P0-213-2).

import { describe, expect, test } from "vitest";

import type { AuditPeriod } from "@/lib/api";

import { pickMostRecentInProgress } from "./in-progress-audit-pill";

function makePeriod(overrides: Partial<AuditPeriod> = {}): AuditPeriod {
  return {
    id: "00000000-0000-0000-0000-000000000000",
    name: "Test period",
    framework_version_id: "00000000-0000-0000-0000-000000000000",
    period_start: "2026-01-01",
    period_end: "2026-03-31",
    status: "open",
    created_by: "test@example.invalid",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

describe("pickMostRecentInProgress", () => {
  test("returns null when the list is empty", () => {
    expect(pickMostRecentInProgress([])).toBeNull();
  });

  test("returns null when no period is active (all frozen)", () => {
    const periods = [
      makePeriod({ id: "a", status: "frozen" }),
      makePeriod({ id: "b", status: "frozen" }),
    ];
    expect(pickMostRecentInProgress(periods)).toBeNull();
  });

  test("returns the sole active period when only one matches", () => {
    const periods = [
      makePeriod({ id: "a", status: "frozen" }),
      makePeriod({
        id: "b",
        status: "open",
        name: "Q2 2026",
        period_start: "2026-04-01",
      }),
      makePeriod({ id: "c", status: "frozen" }),
    ];
    const pick = pickMostRecentInProgress(periods);
    expect(pick?.id).toBe("b");
    expect(pick?.name).toBe("Q2 2026");
  });

  test("returns the period with the latest period_start when multiple match", () => {
    const periods = [
      makePeriod({
        id: "older",
        status: "open",
        name: "Q1 2026",
        period_start: "2026-01-01",
      }),
      makePeriod({
        id: "newer",
        status: "open",
        name: "Q2 2026",
        period_start: "2026-04-01",
      }),
      makePeriod({
        id: "middle",
        status: "open",
        name: "Q1.5 2026",
        period_start: "2026-02-15",
      }),
    ];
    const pick = pickMostRecentInProgress(periods);
    expect(pick?.id).toBe("newer");
  });

  test("ignores non-active periods when comparing dates", () => {
    // A 'frozen' period starting LATER than the active one must
    // not displace the active winner. The filter happens before
    // the sort.
    const periods = [
      makePeriod({
        id: "in-prog",
        status: "open",
        period_start: "2026-04-01",
      }),
      makePeriod({
        id: "frozen-later",
        status: "frozen",
        period_start: "2026-07-01",
      }),
    ];
    const pick = pickMostRecentInProgress(periods);
    expect(pick?.id).toBe("in-prog");
  });

  test("handles a single-element list correctly", () => {
    const periods = [makePeriod({ id: "solo", status: "open" })];
    expect(pickMostRecentInProgress(periods)?.id).toBe("solo");
  });
});
