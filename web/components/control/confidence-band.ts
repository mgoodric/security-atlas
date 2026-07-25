// Slice 482 — confidence-band classification + styling for the
// coverage-strength rollup (canvas §3.2).
//
// The backend is the source of truth for the requirement-level
// `confidence_band` (GET /v1/requirements/{id}/coverage — see
// internal/api/ucfcoverage/rollup.go). This module mirrors the SAME
// thresholds so the per-requirement coverage rows on the control detail
// view (which carry a numeric per-row `coverage` from slice 256, not the
// whole-requirement rollup) can show a matching band label without a
// round-trip. The thresholds are kept byte-identical to the Go side; if
// the Go thresholds change, this file changes in the same slice (JUDGMENT
// — decisions log).
//
// Pure (no DOM, no React) so it gets vitest unit coverage (AC-5 / AC-8
// frontend half). The badge component delegates to classifyBand so
// rendering tests don't re-derive the cut points.

export type ConfidenceBand = "uncovered" | "weak" | "partial" | "strong";

// Cut points mirror internal/api/ucfcoverage/rollup.go:
//   (0, 0.5)   → weak
//   [0.5, 0.8) → partial
//   [0.8, 1.0] → strong
// null / no-coverage → uncovered.
const WEAK_CEIL = 0.5;
const PARTIAL_CEIL = 0.8;

/**
 * classifyBand maps a per-row coverage value to a band label, matching
 * the backend rollup classification.
 *
 * @param coverage  the per-row coverage number in [0, 1], or null when
 *                  the row is out of scope / has no effectiveness data.
 *                  null maps to "uncovered" — a foreign-scope or no-data
 *                  row is NOT covered (mirrors the Go hasAnyCoverage=false
 *                  path and the slice 256 null contract).
 * @returns         the matching ConfidenceBand.
 */
export function classifyBand(
  coverage: number | null | undefined,
): ConfidenceBand {
  if (coverage === null || coverage === undefined || Number.isNaN(coverage)) {
    return "uncovered";
  }
  const v = Math.max(0, Math.min(1, coverage));
  if (v < WEAK_CEIL) return "weak";
  if (v < PARTIAL_CEIL) return "partial";
  return "strong";
}

export type BandStyle = {
  // Tailwind badge classes.
  badge: string;
  // Human gloss shown in the badge title attribute.
  label: string;
};

const BAND_STYLES: Record<ConfidenceBand, BandStyle> = {
  strong: {
    badge: "bg-emerald-50 text-emerald-700 ring-1 ring-inset ring-emerald-200",
    label: "Strong coverage",
  },
  partial: {
    badge: "bg-amber-50 text-amber-700 ring-1 ring-inset ring-amber-200",
    label: "Partial coverage",
  },
  weak: {
    badge: "bg-orange-50 text-orange-700 ring-1 ring-inset ring-orange-200",
    label: "Weak coverage",
  },
  uncovered: {
    badge: "bg-slate-100 text-slate-500 ring-1 ring-inset ring-slate-200",
    label: "Uncovered",
  },
};

// bandStyle resolves a band label to its styling. Total over the union,
// so there is no fallback branch to test.
export function bandStyle(band: ConfidenceBand): BandStyle {
  return BAND_STYLES[band];
}
