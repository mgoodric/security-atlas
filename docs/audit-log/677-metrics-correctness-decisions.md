# 677 — Metrics correctness pass (freshness consistency, count labeling, neutral no-target badge) — decisions log

- detection_tier_actual: manual_review
- detection_tier_target: playwright

All three defects (ATLAS-020 / 021 / 023) were caught at **manual_review** —
the 2026-06-10 demo-tenant UI audit on build `2a3805b`. They SHOULD have been
caught at the **playwright** tier: each is a cross-surface UI-consistency or
badge-rendering defect that a hermetic e2e assertion (freshness identical across
the dashboard widget and the metrics KPI; no-target → neutral badge; count
labels disambiguated) would have flagged the moment the surfaces drifted. The
`metrics-dashboard.spec.ts` was fully commented-out (quarantined pending the
slice-082 seed harness), so the metrics surface had no live e2e gate at all.
This slice adds `e2e/metrics-correctness-consistency.spec.ts` — a HERMETIC
(BFF-route-mocked, slice-594 pattern) spec that needs no seed harness — so the
three pins are now enforced at the cheapest tier that can see a cross-surface
contradiction. `actual=manual_review, target=playwright` → a playwright-coverage
gap on the metrics surface, now closed.

---

## Context

Three metric-display correctness defects, bundled because they share the metrics
surface and (for ATLAS-020) an overlapping computation:

| Sub       | Symptom (demo tenant, build 2a3805b)                                                                                                             |
| --------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| ATLAS-020 | Dashboard freshness widget "100% within window, 50/50 fresh"; Metrics view "Evidence freshness = 0.0%". Two views, one tenant, contradiction.    |
| ATLAS-021 | Dashboard freshness widget over 50 ("0 of 50"); Evidence ledger 200 ("Showing 50 of 200"). 4× disagreement, reads as a bug.                      |
| ATLAS-023 | A metric with value 0.0% / 0 and "no target set" still renders a green "on target" pill. A 0% board KPI shown green is misleading to executives. |

Root causes (verified by reading both code paths before touching either):

- **ATLAS-020.** Both surfaces ultimately derive from the SAME `evidence_freshness`
  read model (one row per (tenant, control)). But the dashboard widget reads it
  LIVE (`GET /v1/evidence/freshness` → `computeFreshnessPct`), while the metrics
  view rendered the latest STORED metric observation for `evidence_freshness_pct`.
  The latest stored observation was a point-in-time 0.0% snapshot captured by the
  metrics scheduler BEFORE slice 671 wired seed-evaluation to populate
  `evidence_freshness`. Same definition (`fresh/total`), but one surface read
  live state and the other read a stale stored snapshot — hence 100% vs 0.0%.
- **ATLAS-021.** The dashboard freshness panel counts CONTROLS (latest-per-control
  freshness verdicts, ~50). The evidence ledger counts all append-only evidence
  RECORDS (~200). Genuinely different populations (invariant #2: many evidence
  rows roll up to one latest-per-control verdict), correctly so. The prior copy
  said "evidence records", which read as the same population as the ledger — a
  labeling problem, not a count bug.
- **ATLAS-023.** `thresholdBadgeColor` (lib/api/metrics.ts) returned `"green"`
  for `!target` and for a target with no `target_value`. Green for a metric with
  nothing to measure against.

---

## D1 — ATLAS-023 badge default: no target → NEUTRAL, never green

**Decision.** `thresholdBadgeColor` returns `"neutral"` (not `"green"`) when there
is no target row, and when a target row has no `target_value`. Green is reserved
exclusively for value-meets-target.

**Why.** The board KPI surface is read by non-technical executives at face value
(the same asymmetric-hallucination-cost reasoning the canvas applies to board
narratives). "On target" green with no target configured is an affirmative claim
the data does not support — actively misleading, and the worse failure mode than
a muted "no target" that prompts the operator to configure one. Pattern-matched
to the existing `value === undefined → neutral` rule already in the function (no
observation is already neutral; no target is the same class of "nothing to
assert"). Options considered: (a) green default [rejected — the defect],
(b) yellow/warning default [rejected — implies a measured shortfall, also a false
claim], (c) neutral [chosen]. Anti-criterion honored: never green for a
genuinely-unmet or no-target metric.

**Confidence: high.** The rule is unambiguous and the pure function is fully
unit-tested across every branch.

## D2 — ATLAS-023 label nuance: distinguish "no data" from "no target"

**Decision.** Both neutral cases render the SAME muted badge, but the COPY
differs: no observation → "no data"; a value exists but no target → "no target".
A new pure helper `neutralBadgeLabel(parsedValue, target)` in lib/api/metrics.ts
owns the label choice; the color decision stays entirely in `thresholdBadgeColor`.

**Why.** Collapsing both to one label ("no data") would mislabel a metric that
HAS a current value but simply lacks a configured target. Splitting the label is
honest about the cause without introducing a second color (which would dilute the
green/yellow/red/neutral vocabulary). Pure helper so it is node-testable (the
vitest tier is node-only per slice 353 Q-3) and the component stays declarative.

**Confidence: high.**

## D3 — ATLAS-020 reconciliation: ONE live source of truth, shared by both surfaces

**Decision.** Introduce `web/lib/api/freshness-consistency.ts` exporting the single
canonical definition `freshnessPctFromReport(report)`. The dashboard subtitle's
`computeFreshnessPct` DELEGATES to it; the metrics board KPI card reads the LIVE
`FreshnessReport` (the same `/api/dashboard/freshness` BFF, sharing the dashboard's
TanStack query key) through the SAME function for its headline value + badge. The
stored-observation series still backs the trend sparkline.

**Why.** The contradiction was staleness-of-snapshot, not a definitional
difference — both surfaces already meant `fresh/total`. The durable fix is to
remove the second read path: one function, one source, so the two surfaces
_cannot_ disagree by construction (the AC-2 e2e pins this). Options considered:
(a) recompute/refresh the stored observation [rejected — that is slice-671/scheduler
territory and would not prevent future snapshot drift]; (b) make the metrics card
read live freshness [chosen — definition-preserving, display-only, and removes the
divergence class entirely]. Honors invariant #2 (read-only; no evidence-ledger or
freshness-window write) and the slice anti-criterion (no definition change —
`freshnessPctFromReport` is the same `fresh/total` math, scaled to a percentage).

**Confidence: medium.** The reconciliation is correct and tested, but it is scoped
to the `evidence_freshness_pct` KPI specifically (the one metric where a live read
model exists alongside the stored observation). REVISIT: if other board metrics
later gain a live read model that can drift from their stored observation, the
same pattern should be generalized rather than special-cased per metric id.

## D4 — ATLAS-020 value scaling at the boundary

**Decision.** The metrics card expresses the live freshness value as the same 0-1
FRACTION the slice-076 evaluator emits (`pct / 100`), not the 0-100 integer, so
`formatValue`'s percent path renders it identically to a stored observation.

**Why.** `formatValue` treats a percent value `<= 1` as a fraction (×100) and
`> 1` as already-scaled points. Passing the 0-100 integer would misformat a live
1% as "100.0%". Expressing the live value as a fraction routes it through the
identical path a stored observation uses, eliminating the boundary bug and keeping
the formatting consistent across live vs stored.

**Confidence: high.** Caught during the slice (see detection-tier note below).

## D5 — ATLAS-021 count labeling: label the freshness panel population as "controls"

**Decision.** Relabel the dashboard freshness panel: per-bucket "{fresh}/{total}
controls fresh", and the stale-total line "{stale} of {total} controls have their
latest evidence past its freshness window. (Controls, not ledger records — the
evidence ledger counts every append-only record.)" The panel description now reads
"Per-control freshness verdict (latest evidence per control) by freshness class".

**Why.** The two counts (50 controls vs 200 records) are correct and count
different things; the defect was that the copy implied the same population. Labeling
the unit ("controls") and explicitly disclaiming the ledger-record population
removes the false-contradiction read without reconciling to one number (which would
be wrong — they legitimately differ). Pattern-matched to slice 666 (ATLAS-007), the
sibling count-consistency fix that relabeled `/controls` header vs footer.

**Confidence: high.**

---

## Revisit once in use

1. **D3 generality.** The live-source reconciliation is special-cased to
   `evidence_freshness_pct` (the only metric with a live read model today). If a
   second board metric gains a live read model, generalize the pattern (e.g. a
   per-metric "live source" registry) rather than adding another id check.
2. **D5 wording.** Confirm with a real operator that "controls, not ledger records"
   reads clearly in the board-glance context; tighten if it reads as clutter.
3. **D1 across all surfaces.** Audit any future metric surface (e.g. a v2
   board-pack metrics tile) adopts the neutral-no-target rule rather than
   re-introducing a green default.
4. **Stored freshness observation.** The metrics card now ignores the stale stored
   observation for its HEADLINE value but still uses the series for the sparkline.
   Once the metrics scheduler reliably recomputes `evidence_freshness_pct` on a
   populated read model (slice-671 follow-on), confirm the stored series and the
   live value converge so the sparkline's last point matches the headline.

## Confidence summary

| Decision                        | Confidence |
| ------------------------------- | ---------- |
| D1 neutral-no-target badge      | high       |
| D2 no-data vs no-target label   | high       |
| D3 one live freshness source    | medium     |
| D4 fraction scaling at boundary | high       |
| D5 controls-vs-records labeling | high       |

## Detection-tier note (bug caught DURING the slice)

D4 (the 0-100-integer-vs-fraction formatting boundary bug) was caught at
`manual_review` during this slice — I spotted that `formatValue(liveFreshnessPct)`
would misformat a live 1% as 100% while writing the card wiring, before any test
ran. The cheapest tier that should catch it is `unit` (a `formatValue` table test
over the boundary values). The fraction fix (D4) plus the existing format tests
cover it going forward; the e2e ATLAS-020 assertion (`100.0%`) also exercises the
path end-to-end.
