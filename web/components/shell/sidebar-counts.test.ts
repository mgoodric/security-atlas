// Slice 214 — vitest coverage for the pure helper that powers the
// sidebar Risks badge.
//
// The badge components themselves (`ControlsCountBadge`,
// `RisksCountBadge`) integrate TanStack Query + the Next.js client
// boundary and are hostile to a vitest unit test — that surface is
// owned by the Playwright e2e (`web/e2e/audits-header.spec.ts`), in
// the same shape slice 213 settled on for `in-progress-audit-pill`.
// The pure counting function is unit-testable here.
//
// AC-2: "Risks badge in rose when critical-open count > 0, hidden
// when 0". The badge component reads the count from this helper;
// pinning the helper pins the silent-absence boundary.
//
// JUDGMENT call recorded in
// `docs/audit-log/214-sidebar-item-counts-decisions.md` D1: the spec's
// "open critical" phrasing maps to the canonical slice-100
// `severity >= 15` "high" tier (which renders rose in
// `severityClasses`). The schema carries neither a `status` column nor
// a `critical` band on the risk wire shape; the high tier is the
// load-bearing translation.

import { describe, expect, test } from "vitest";

import type { Risk } from "@/lib/api/risks";

import {
  countHighSeverityRisks,
  highSeverityBadgeLabel,
  HIGH_SEVERITY_BADGE_MARKER,
} from "./sidebar-counts";

function makeRisk(overrides: Partial<Risk> = {}): Risk {
  return {
    id: "00000000-0000-0000-0000-000000000000",
    title: "Test risk",
    description: "",
    category: "operational",
    methodology: "5x5",
    inherent_score: null,
    treatment: "mitigate",
    treatment_owner: "",
    residual_score: null,
    accepter: "",
    instrument_reference: "",
    linked_control_ids: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    themes: [],
    severity: 0,
    ...overrides,
  };
}

describe("countHighSeverityRisks", () => {
  test("returns 0 when the list is empty", () => {
    expect(countHighSeverityRisks([])).toBe(0);
  });

  test("returns 0 when no row crosses the high-tier threshold", () => {
    const risks = [
      makeRisk({ severity: 0 }),
      makeRisk({ severity: 7 }), // low
      makeRisk({ severity: 14 }), // medium boundary
    ];
    expect(countHighSeverityRisks(risks)).toBe(0);
  });

  test("counts only rows with severity >= 15 (the canonical 'high' / rose tier)", () => {
    const risks = [
      makeRisk({ severity: 14 }), // medium — excluded
      makeRisk({ severity: 15 }), // high — included
      makeRisk({ severity: 20 }), // high — included
      makeRisk({ severity: 25 }), // high (max) — included
    ];
    expect(countHighSeverityRisks(risks)).toBe(3);
  });

  test("treats severity exactly 14 as below the high threshold (boundary)", () => {
    // The slice-100 filters.ts uses `severity >= 15`; verify the
    // helper does NOT silently shift the boundary.
    expect(countHighSeverityRisks([makeRisk({ severity: 14 })])).toBe(0);
  });

  test("treats severity exactly 15 as inside the high threshold (boundary)", () => {
    expect(countHighSeverityRisks([makeRisk({ severity: 15 })])).toBe(1);
  });

  test("counts mixed lists correctly", () => {
    const risks = [
      makeRisk({ severity: 0 }), // none
      makeRisk({ severity: 5 }), // low
      makeRisk({ severity: 12 }), // medium
      makeRisk({ severity: 16 }), // high
      makeRisk({ severity: 22 }), // high
    ];
    expect(countHighSeverityRisks(risks)).toBe(2);
  });
});

// Slice 681 / ATLAS-036 — the badge label must read as "high-severity",
// not a total count, in BOTH the screen-reader (aria-label) and the
// sighted-hover (title) surfaces.
describe("highSeverityBadgeLabel", () => {
  test("always names the count as high-severity (not a total)", () => {
    expect(highSeverityBadgeLabel(10)).toBe("10 high-severity risks");
    expect(highSeverityBadgeLabel(10)).toContain("high-severity");
    expect(highSeverityBadgeLabel(10)).not.toMatch(/total/i);
  });

  test("uses the singular noun for exactly one risk", () => {
    expect(highSeverityBadgeLabel(1)).toBe("1 high-severity risk");
  });

  test("uses the plural noun for zero and for many", () => {
    expect(highSeverityBadgeLabel(0)).toBe("0 high-severity risks");
    expect(highSeverityBadgeLabel(21)).toBe("21 high-severity risks");
  });

  test("exposes a non-empty visual marker glyph distinct from a digit", () => {
    // The marker disambiguates the rose count visually; it must not be a
    // numeral (which would read as part of the count).
    expect(HIGH_SEVERITY_BADGE_MARKER.length).toBeGreaterThan(0);
    expect(HIGH_SEVERITY_BADGE_MARKER).not.toMatch(/[0-9]/);
  });
});
