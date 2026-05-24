// Slice 256 — pure helpers for the coverage column.
//
// Two responsibilities:
//   1. formatCoverage: turn a `number | null` into the displayed cell
//      string ("0.94" or "n/a"). Two decimals; null renders "n/a" so
//      the out-of-scope rendering is the spec's exact mockup-line-224
//      string. Never returns "0" for null — distinguishing "no data /
//      not applicable" from "perfectly failing" is the AC-2 / AC-3
//      contract (slice 256 anti-criterion P0-256-1).
//
//   2. coverageBarPercent: turn a `number | null` (plus the out-of-
//      scope marker) into the strength-bar's fill width as an integer
//      percent. The bar's filled portion equals the COVERAGE value
//      (mockup line 192, 203, 214) — NOT the raw strength. Clamped to
//      [0, 100] so a numerically out-of-range backend value can't break
//      layout. null OR out-of-scope renders 0%.
//
// Both functions are pure (no DOM, no React) so they get vitest unit
// coverage per AC-6. The CoverageTable component delegates to them so
// rendering tests don't have to re-derive the math.

/**
 * formatCoverage renders the coverage cell value.
 *
 * @param coverage  the backend's per-row coverage value: a finite
 *                  [0, 1] number, or null when out of scope / no data.
 *                  The frontend never computes this client-side
 *                  (slice 256 P0-256-1).
 * @returns         "0.94" for numeric coverage (always two decimals,
 *                  matches mockup lines 191, 202, 213). "n/a" for null
 *                  (matches mockup line 224).
 */
export function formatCoverage(coverage: number | null): string {
  if (coverage === null || coverage === undefined) return "n/a";
  if (Number.isNaN(coverage)) return "n/a";
  // Clamp display value to [0, 1] so a slightly-overflowing backend
  // number (floating-point rounding above 1.0) doesn't render "1.01".
  const v = Math.max(0, Math.min(1, coverage));
  return v.toFixed(2);
}

/**
 * coverageBarPercent returns the integer percent the strength bar
 * fills to. The mockup binds the BAR to the COVERAGE value, not the
 * raw strength: an in-scope row with strength 1.00 × effectiveness
 * 0.94 fills the bar to 94%, not 100%. An out-of-scope row, OR a row
 * with null coverage, renders an empty bar (0%) — a 0% bar that says
 * "n/a" reads as "we can't measure this," whereas a filled bar would
 * imply we measured 0.
 *
 * @param coverage     numeric [0, 1] in-scope coverage, or null.
 * @param outOfScope   true when the row's framework_version is out of
 *                     scope. Forces 0% regardless of `coverage` so the
 *                     two signals (backend-null AND frontend-known-
 *                     OOS) compose without contradiction.
 * @returns            integer in [0, 100].
 */
export function coverageBarPercent(
  coverage: number | null,
  outOfScope: boolean,
): number {
  if (outOfScope) return 0;
  if (coverage === null || coverage === undefined) return 0;
  if (Number.isNaN(coverage)) return 0;
  const clamped = Math.max(0, Math.min(1, coverage));
  return Math.round(clamped * 100);
}
