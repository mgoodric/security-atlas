// Slice 213 — vitest coverage for the in-progress audit pill helper.
//
// The component itself integrates TanStack Query + Next.js server/
// client boundary and is hostile to a vitest unit test; that surface
// is owned by the Playwright e2e (`web/e2e/audits-header.spec.ts`).
// The pure picker function is unit-testable here.
//
// AC-3: "filters to `status === 'in_progress'`, renders the most-
// recently-started period as an amber pill". This file pins the
// "most-recently-started" + "filter narrows" + "zero match returns
// null" branches.
//
// AC-5 (the "absent otherwise" half): when zero periods match
// status='in_progress', the helper returns null and the component
// returns null — silent-absence (P0-213-2).

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
    status: "in_progress",
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

  test("returns null when no period has status='in_progress'", () => {
    const periods = [
      makePeriod({ id: "a", status: "open" }),
      makePeriod({ id: "b", status: "frozen" }),
      makePeriod({ id: "c", status: "closed" }),
    ];
    expect(pickMostRecentInProgress(periods)).toBeNull();
  });

  test("returns the sole in_progress period when only one matches", () => {
    const periods = [
      makePeriod({ id: "a", status: "open" }),
      makePeriod({
        id: "b",
        status: "in_progress",
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
        status: "in_progress",
        name: "Q1 2026",
        period_start: "2026-01-01",
      }),
      makePeriod({
        id: "newer",
        status: "in_progress",
        name: "Q2 2026",
        period_start: "2026-04-01",
      }),
      makePeriod({
        id: "middle",
        status: "in_progress",
        name: "Q1.5 2026",
        period_start: "2026-02-15",
      }),
    ];
    const pick = pickMostRecentInProgress(periods);
    expect(pick?.id).toBe("newer");
  });

  test("ignores non-in_progress periods when comparing dates", () => {
    // A 'frozen' period starting LATER than the in_progress one must
    // not displace the in_progress winner. The filter happens before
    // the sort.
    const periods = [
      makePeriod({
        id: "in-prog",
        status: "in_progress",
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
    const periods = [makePeriod({ id: "solo", status: "in_progress" })];
    expect(pickMostRecentInProgress(periods)?.id).toBe("solo");
  });
});
