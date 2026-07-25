// Slice 677 / ATLAS-020 — single source of truth for the "evidence
// freshness %" figure shared by the dashboard freshness widget and the
// metrics-view board KPI.
//
// THE BUG: the dashboard widget read the LIVE freshness read model
// (`GET /v1/evidence/freshness`) and reported "100% within window", while
// the metrics view rendered the latest STORED metric observation for
// `evidence_freshness_pct`, which was a point-in-time 0.0% snapshot
// captured by the metrics scheduler BEFORE the seed evaluation wired up
// in slice 671 populated `evidence_freshness`. Same tenant, two numbers,
// because one surface read live state and the other read a stale stored
// snapshot.
//
// THE FIX (display + consistency only — invariant #2 read-only, and the
// slice anti-criterion forbids changing the freshness-window definition):
// both surfaces now compute the headline freshness % from the SAME live
// `FreshnessReport` via the ONE function below. The dashboard subtitle
// delegates to it; the metrics board card for `evidence_freshness_pct`
// reads it instead of the stored observation for its headline value and
// badge. The stored observation series still backs the trend sparkline,
// but the two surfaces can no longer disagree on the current number
// because they read one source through one definition.
//
// Definition: fraction of (tenant, control) freshness rows whose latest
// evidence is inside the control's freshness window, expressed as an
// integer 0-100. `fresh = total - stale`, so `100 * (1 - stale/total)` is
// the same definition the slice-076 evaluator uses (`fresh / total`),
// scaled to a percentage. A tenant with no freshness rows yet (total 0)
// returns null — the caller renders an honest empty state, never the
// meaningless "100% of 0".

import type { FreshnessReport } from "@/lib/api/dashboard";

// The catalog id of the board-level evidence-freshness KPI
// (catalogs/metrics/evidence-freshness.yaml). The metrics board card
// special-cases this id to read live freshness rather than the stored
// observation, keeping it consistent with the dashboard widget.
export const EVIDENCE_FRESHNESS_METRIC_ID = "evidence_freshness_pct";

/**
 * freshnessPctFromReport turns a FreshnessReport's (total, total_stale)
 * counts into the integer "% within window", 0-100. Returns null when
 * total <= 0 (no freshness rows yet) so the caller renders an empty
 * state instead of "100% of 0".
 *
 * Defensive against bad inputs: negative total and total_stale > total
 * both clamp rather than throwing.
 */
export function freshnessPctFromReport(
  report: Pick<FreshnessReport, "total" | "total_stale"> | undefined | null,
): number | null {
  if (!report) return null;
  const total = report.total;
  if (typeof total !== "number" || total <= 0) return null;
  const stale = Math.max(0, Math.min(report.total_stale ?? 0, total));
  return Math.round(100 * (1 - stale / total));
}
