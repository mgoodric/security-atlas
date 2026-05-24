// Slice 256 — vitest unit coverage for the pure coverage helpers.
//
// Tests live next to the source (existing convention in
// `web/components/control/strm.ts` companion files; vitest auto-picks
// `*.test.ts`). The helpers are pure so this is the AC-6 surface:
//
//   - formatCoverage: numeric → "0.94"; null → "n/a"; NaN → "n/a";
//     out-of-range → clamped; rounding → two decimals.
//   - coverageBarPercent: numeric in-scope → percent; out-of-scope → 0%;
//     null → 0%; NaN → 0%; out-of-range → clamped.

import { describe, expect, it } from "vitest";

import { coverageBarPercent, formatCoverage } from "./coverage";

describe("formatCoverage", () => {
  it("renders a numeric coverage to two decimals", () => {
    expect(formatCoverage(0.94)).toBe("0.94");
    expect(formatCoverage(0.6666666)).toBe("0.67");
    expect(formatCoverage(0)).toBe("0.00");
    expect(formatCoverage(1)).toBe("1.00");
  });

  it("renders null as 'n/a'", () => {
    expect(formatCoverage(null)).toBe("n/a");
  });

  it("renders NaN as 'n/a' (defense in depth — backend wire shape disallows it)", () => {
    expect(formatCoverage(Number.NaN)).toBe("n/a");
  });

  it("clamps an out-of-range coverage value to [0, 1] before formatting", () => {
    // A floating-point >1 (e.g. 1.0000001) must not render as "1.00…01".
    expect(formatCoverage(1.0001)).toBe("1.00");
    expect(formatCoverage(-0.01)).toBe("0.00");
  });
});

describe("coverageBarPercent", () => {
  it("returns the rounded integer percent for a numeric in-scope coverage", () => {
    expect(coverageBarPercent(0.94, false)).toBe(94);
    expect(coverageBarPercent(0.85, false)).toBe(85);
    expect(coverageBarPercent(0.6666, false)).toBe(67);
  });

  it("returns 0 when out of scope, regardless of coverage value", () => {
    // P0-1: out-of-scope rows render an empty bar even if the backend
    // somehow ships a numeric coverage. The two signals MUST compose
    // without contradiction.
    expect(coverageBarPercent(0.94, true)).toBe(0);
    expect(coverageBarPercent(null, true)).toBe(0);
  });

  it("returns 0 when coverage is null (no effectiveness data)", () => {
    expect(coverageBarPercent(null, false)).toBe(0);
  });

  it("returns 0 for NaN (defense in depth)", () => {
    expect(coverageBarPercent(Number.NaN, false)).toBe(0);
  });

  it("clamps an out-of-range coverage value to [0, 100]", () => {
    expect(coverageBarPercent(1.5, false)).toBe(100);
    expect(coverageBarPercent(-0.1, false)).toBe(0);
  });

  it("AC-6 binding — bar fills at COVERAGE, not strength", () => {
    // The mockup binds the bar's filled portion to coverage (lines
    // 192/203/214). With strength=1.0 and pass_rate=0.94, the row's
    // coverage is 0.94 and the bar must read 94%, not 100%.
    const strength = 1.0;
    const passRate = 0.94;
    const coverage = strength * passRate;
    expect(coverageBarPercent(coverage, false)).toBe(94);
  });
});
