// Slice 214 — vitest coverage for the pure helpers behind the sidebar
// badges.
//
// The badge components themselves (`ControlsCountBadge`,
// `RisksCountBadge`) integrate TanStack Query + the Next.js client
// boundary and are hostile to a vitest unit test — that surface is
// owned by the Playwright e2e (`web/e2e/risks-sort-detail-badge.spec.ts`),
// in the same shape slice 213 settled on for `in-progress-audit-pill`.
//
// #2490: the Risks badge's count moved to the platform
// (`?severity_band=high`), so the client-side `countHighSeverityRisks`
// and its threshold tests are gone; the band boundaries are pinned in Go
// (`TestSeverityBandRangeMatchesSeverityBand`,
// `TestSeverityBand_MatchesWireSeverity`).

import { describe, expect, test } from "vitest";

import {
  approvalsBadgeLabel,
  highSeverityBadgeLabel,
  HIGH_SEVERITY_BADGE_MARKER,
} from "./sidebar-counts";

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

// #2119 — the Approvals badge says what the number counts.
describe("approvalsBadgeLabel", () => {
  test("names the count as waiting for the caller", () => {
    expect(approvalsBadgeLabel(1)).toBe("1 waiting for your approval");
    expect(approvalsBadgeLabel(4)).toBe("4 waiting for your approval");
  });
});
