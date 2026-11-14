// Slice 222 / #2056 — unit coverage for the posture coverage-definition
// caption.
//
// The caption is rendered inside the PostureTiles component (board-pack
// posture section). The component itself is not vitest-rendered (web/
// vitest config disallows component rendering — slice 069 / 130). The
// caption text lives in posture-coverage-caption.ts (pure TS) per the
// slice 183 precedent. #2056 points the pack's posture at the dashboard's
// per-framework posture query, so the caption pins that definition rather
// than the archived mockup's wording.

import { describe, expect, test } from "vitest";

import { POSTURE_COVERAGE_CAPTION } from "./posture-coverage-caption";

describe("POSTURE_COVERAGE_CAPTION", () => {
  test("states the dashboard's per-framework definition", () => {
    const expected =
      "Coverage definition: the share of each framework's in-scope requirements (those its activated framework scope does not exclude) with at least one control on a mapped SCF anchor. The same figure the dashboard shows; the trend and baseline are over the last 90 days.";
    expect(POSTURE_COVERAGE_CAPTION).toBe(expected);
  });

  test("names the framework-scope exclusion and the dashboard (invariant 5)", () => {
    expect(POSTURE_COVERAGE_CAPTION).toMatch(/activated framework scope/);
    expect(POSTURE_COVERAGE_CAPTION).toMatch(/dashboard/);
    expect(POSTURE_COVERAGE_CAPTION).not.toMatch(/pass rate/);
  });
});
