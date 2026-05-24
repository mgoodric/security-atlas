// Slice 218 — unit coverage for the board-pack breadcrumb helpers.
//
// The vitest config (web/vitest.config.ts) is node-env / no-JSX so the
// component itself cannot be rendered here. We pin the pure logic that
// the .tsx component renders from — segment array shape, segment count,
// link target, period-label parsing — per the slice 219
// (pack-header-meta.test.ts) precedent.

import { describe, expect, test } from "vitest";

import {
  packBreadcrumbSegments,
  periodLabel,
} from "./pack-breadcrumb-segments";

describe("packBreadcrumbSegments", () => {
  test("returns exactly two segments (AC-1: at least 2)", () => {
    const segs = packBreadcrumbSegments("2026-03-31");
    expect(segs.length).toBe(2);
  });

  test("first segment is 'Board packs' linking to /board-packs (AC-1)", () => {
    const [parent] = packBreadcrumbSegments("2026-03-31");
    expect(parent.label).toBe("Board packs");
    expect(parent.href).toBe("/board-packs");
    expect(parent.testId).toBe("pack-breadcrumb-segment-parent");
  });

  test("second segment is the period label as plain text (no href)", () => {
    const [, current] = packBreadcrumbSegments("2026-03-31");
    expect(current.label).toBe("Q1 2026");
    expect(current.href).toBeUndefined();
    expect(current.testId).toBe("pack-breadcrumb-segment-current");
  });

  test("does NOT fabricate a tenant-name segment (P0-218-2)", () => {
    // Regression guard. The mockup at Plans/mockups/board-pack.html
    // lines 27-30 ships a "Sentinel Labs" → "Board reports" → period
    // chain. We deliberately drop both fabricated segments — there's
    // no session-bound tenant name to render honestly, and there's no
    // "Board reports" route on main (it would be a dead anchor; the
    // slice-178 audit harness flags those as HONESTY-GAPs).
    const labels = packBreadcrumbSegments("2026-03-31").map((s) => s.label);
    expect(labels).not.toContain("Sentinel Labs");
    expect(labels).not.toContain("Board reports");
  });

  test("non-quarter-end periodEnd falls back to the raw date", () => {
    const segs = packBreadcrumbSegments("2026-05-15");
    expect(segs[1].label).toBe("2026-05-15");
  });
});

describe("periodLabel", () => {
  test("Q1: March 31 maps to Q1 <year>", () => {
    expect(periodLabel("2026-03-31")).toBe("Q1 2026");
  });

  test("Q2: June 30 maps to Q2 <year>", () => {
    expect(periodLabel("2026-06-30")).toBe("Q2 2026");
  });

  test("Q3: September 30 maps to Q3 <year>", () => {
    expect(periodLabel("2026-09-30")).toBe("Q3 2026");
  });

  test("Q4: December 31 maps to Q4 <year>", () => {
    expect(periodLabel("2026-12-31")).toBe("Q4 2026");
  });

  test("non-quarter-end date falls back to the raw string (no fabrication)", () => {
    expect(periodLabel("2026-05-15")).toBe("2026-05-15");
    expect(periodLabel("2026-04-30")).toBe("2026-04-30");
  });

  test("malformed date falls back to the raw string", () => {
    expect(periodLabel("not-a-date")).toBe("not-a-date");
    expect(periodLabel("")).toBe("");
  });
});
