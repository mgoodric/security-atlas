// Slice 218 — board-pack detail breadcrumb chain helpers.
//
// Pure-logic module per the slice 219 (pack-header-meta.ts) + slice 222
// (posture-coverage-caption.ts) precedent: web/vitest.config.ts is
// node-env / no-JSX, so the testable surface is the segment shape this
// module exposes. The `pack-breadcrumb.tsx` component imports the
// segment array; the `pack-breadcrumb-segments.test.ts` file pins the
// contract.
//
// Sibling-file note: `pack-breadcrumb.tsx` is the React component; this
// module is the pure-logic peer. The `-segments` suffix mirrors the
// established `pack-header.tsx` + `pack-header-meta.ts` pattern in this
// directory, which avoids the base-name collision the TypeScript
// `moduleResolution: bundler` resolver creates when a `.ts` and `.tsx`
// file share a stem.
//
// Honesty discipline (slice 218 spec, P0-218-2):
//   * Every segment links to a real route OR is plain text derived from
//     pack data.
//   * NO fabricated tenant-name segment (the mockup at
//     Plans/_archive/mockups/board-pack.html lines 27-30 ships a "Sentinel Labs"
//     segment; we deliberately drop it — there's no session-bound tenant
//     name to render honestly at the time of build).
//   * NO fabricated parent "Board reports" segment (the mockup line 30
//     ships it; no such route exists on main, so it would be a dead
//     anchor — slice-178 HONESTY-GAP heuristic flags those).
//
// Result: a 2-segment chain — `Board packs` (link to /board-packs) and
// the current pack's period label (plain text, derived from periodEnd).

export type PackBreadcrumbSegment = {
  /** Display label, e.g. "Board packs" or "Q1 2026". */
  label: string;
  /** If present, the segment renders as a link to this route. If
   * undefined, the segment is plain text (the current location — the
   * conventional last-segment shape). */
  href?: string;
  /**
   * `data-testid` token for the segment node. Stable identifier so the
   * slice-178 audit harness and Playwright specs can pin the contract.
   */
  testId: string;
};

/**
 * packBreadcrumbSegments returns the breadcrumb chain for the board-pack
 * detail page. The shape is:
 *
 *   [ { label: "Board packs", href: "/board-packs", testId: ... },
 *     { label: <periodLabel>, testId: ... } ]
 *
 * The current-location segment carries NO href (rendered as plain text);
 * this is the standard breadcrumb pattern and matches the mockup's
 * `text-slate-900 font-medium` styling for the trailing segment.
 */
export function packBreadcrumbSegments(
  periodEnd: string,
): PackBreadcrumbSegment[] {
  return [
    {
      label: "Board packs",
      href: "/board-packs",
      testId: "pack-breadcrumb-segment-parent",
    },
    {
      label: periodLabel(periodEnd),
      testId: "pack-breadcrumb-segment-current",
    },
  ];
}

/**
 * periodLabel derives a "Q1 2026"-style label from YYYY-MM-DD when the
 * date is a calendar-quarter end (Mar 31 / Jun 30 / Sep 30 / Dec 31).
 * Otherwise it falls back to the raw date — no fabrication.
 *
 * Hoisted from `pack-header.tsx` (where it was a private function in
 * slice 043) into this module so the breadcrumb and the cover header
 * share one canonical implementation.
 */
export function periodLabel(periodEnd: string): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(periodEnd);
  if (!match) return periodEnd;
  const [, year, month, day] = match;
  const quarter = quarterFromMonthDay(month, day);
  return quarter ? `${quarter} ${year}` : periodEnd;
}

function quarterFromMonthDay(month: string, day: string): string | null {
  if (month === "03" && day === "31") return "Q1";
  if (month === "06" && day === "30") return "Q2";
  if (month === "09" && day === "30") return "Q3";
  if (month === "12" && day === "31") return "Q4";
  return null;
}
