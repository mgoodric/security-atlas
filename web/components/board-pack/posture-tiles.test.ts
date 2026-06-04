// Slice 222 — unit coverage for the posture coverage-definition caption.
//
// The caption is rendered inside the PostureTiles component (board-pack
// posture section). The component itself is not vitest-rendered (web/
// vitest config disallows component rendering — slice 069 / 130). The
// caption text lives in posture-coverage-caption.ts (pure TS) per the
// slice 183 precedent (calendar/link-for.ts split out of its sibling
// view modules for the same reason). This unit pins it to the
// constitutional methodology string from Plans/_archive/mockups/board-pack.html
// §01 (lines 146–148).
//
// Why this is sufficient coverage for AC-4: the wire shape that matters
// (the exact string the board / auditor reads) is locked by this unit.
// Any drift between the rendered DOM and the constant would require
// changing the import or hard-coding the string a second time — both
// are obvious in PR review. The render-path itself (component is mounted
// on key==="posture" via SectionStructured) is a single switch case in
// board-packs/[id]/page.tsx; its routing is gated by the page
// integration that ships with slice 043.

import { describe, expect, test } from "vitest";

import { POSTURE_COVERAGE_CAPTION } from "./posture-coverage-caption";

describe("POSTURE_COVERAGE_CAPTION", () => {
  test("matches the constitutional mockup string verbatim", () => {
    // The string below is copy-pasted from Plans/_archive/mockups/board-pack.html
    // lines 146–148. Any divergence here is the bug. Do NOT relax this
    // by normalizing whitespace or punctuation — the methodology
    // sentence is audit-trail content (canvas §5.5 invariant 5).
    const expected =
      "Coverage definition: weighted SCF-anchored evidence pass rate intersected with each framework's scope predicate, over the period. Methodology unchanged from prior quarter.";
    expect(POSTURE_COVERAGE_CAPTION).toBe(expected);
  });

  test("names the FrameworkScope intersection (invariant 5)", () => {
    // Soft assertion that the caption keeps naming the load-bearing
    // concept. If a future edit drops "intersected" or "scope predicate"
    // we want CI to surface that intentionally rather than silently.
    expect(POSTURE_COVERAGE_CAPTION).toMatch(/intersected/);
    expect(POSTURE_COVERAGE_CAPTION).toMatch(/scope predicate/);
  });
});
