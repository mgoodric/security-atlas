# Slice 482 — Coverage-strength rollup + confidence-band decisions log

Slice type: JUDGMENT. The rollup formula, the per-anchor combination
rule, the freshness-weighting choice, and the confidence-band thresholds
and labels are subjective product calls. Per the JUDGMENT workflow,
Claude made the calls using best-reasoned, pattern-matched judgment,
recorded them here, and the slice ships when CI is green. Auditors tune
these post-deployment from the "Revisit once in use" list.

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the build. Backend integration tests AC-6/AC-7
passed against real Postgres on the first run; frontend vitest +
type-check + lint passed; the openapi-drift gate stayed green since the
change is additive to an existing route, not a new route.)

---

## Decisions made

### D1 — Rollup formula: best-satisfying-path (MAX over anchors)

**Options considered:**

1. **Weakest-link only** — literal reading of the canvas §3.2 single
   example (`edge_strength × anchor_coverage` for the one mapped anchor).
   Doesn't generalize to the multi-anchor case CC6.6 actually presents
   (NET-04 at 0.8 + IAC-06 at 0.7).
2. **MIN over anchors** — "you're only as covered as your weakest
   mapping." Wrong: a requirement is _satisfied_ if ANY sufficiently
   strong path covers it; a weak alternate mapping should not drag the
   score down.
3. **Sum / average over anchors** — compounds or dilutes; an average
   penalizes a requirement for having a second weak mapping it didn't
   need, and a sum can exceed 1.0.
4. **MAX over anchors of `edge_strength × anchor_coverage`** (chosen) —
   "best satisfying path." The single-anchor case reduces EXACTLY to the
   canvas worked example (0.8 × 1.0 = 0.8); the multi-anchor case credits
   the requirement with its strongest available coverage path.

**Chosen:** option 4.

```
per-anchor term = edge_strength × anchor_coverage
coverage_strength = MAX over anchors of (per-anchor term)
```

where `anchor_coverage` is the BEST (max) tenant-evaluated effectiveness
over the RLS-scoped controls anchored on that anchor, gated by whether
the requirement's framework_version is in that control's FrameworkScope
(slice 256's exact per-control rule, reused).

**Rationale:** pattern-matched to the canvas §3.2 weakest-link example
(single anchor reduces to it) AND to slice 256's per-control coverage
(`strength × 30-day effectiveness × scope predicate`), which already
ships on `/v1/controls/{id}/coverage`. The rollup is the requirement-side
aggregation of the same per-control number. MAX is the natural
"satisfied by the strongest path" semantics for an OR of mappings.

**Confidence:** medium. The MAX-vs-weighted-blend choice is the most
likely thing an auditor pushes back on.

### D2 — anchor_coverage = effectiveness pass rate (NOT pre-multiplied by strength)

The per-anchor coverage term keeps `edge_strength` and the evaluated
state independent until `rollupCoverageStrength` multiplies them, so the
two inputs are independently unit-testable (helpers_test.go) and the
formula reads exactly like the canvas sentence. `anchor_coverage` is the
best 30-day effectiveness pass rate over the tenant's in-scope controls
on that anchor — the same `eval.Engine.Effectiveness` slice 256 uses.

**Confidence:** high (mechanical reuse of an existing, tested primitive).

### D3 — Freshness weighting: NOT included in v1

The slice spec explicitly leaves freshness (slice 016) as an _optional_
input "if the formula weights freshness (decisions-log call)."

**Chosen:** do NOT weight freshness into the rollup in v1.

**Rationale:** the 30-day effectiveness pass rate (`eval.Effectiveness`)
is _already_ a freshness-bounded window — evaluations outside the 30-day
window do not contribute (slice 256). Folding the slice-016
freshness_class in again would double-count staleness and muddy the
single, explainable number the canvas promises. Keeping the formula to
`strength × effectiveness × in-scope` matches the per-control number
operators already see on the control detail view, so the requirement
rollup and the per-control coverage tell a consistent story.

**Confidence:** medium. A future auditor may want a freshness _penalty_
distinct from the window cutoff (e.g. "covered but the evidence is 28
days old" should read weaker than "covered, evidence is 2 days old").
That's a deliberate v2 enrichment, on the revisit list.

### D4 — Confidence-band thresholds + labels

Four bands, with cut points pattern-matched to the canvas worked example:

| Band      | Range            | Rationale                                                                                                                                                                                                                          |
| --------- | ---------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| uncovered | no in-scope path | No tenant-evaluated coverage contributes (or 0 + no path). The threat-model-I default a foreign-tenant requirement resolves to.                                                                                                    |
| weak      | (0, 0.50)        | Coverage exists but the gap is large.                                                                                                                                                                                              |
| partial   | [0.50, 0.80)     | Meaningful but incomplete.                                                                                                                                                                                                         |
| strong    | [0.80, 1.0]      | The canvas §3.2 worked-example 0.8 is the strong-band _floor_ — an ISO requirement covered at 0.8 reads as strongly (not perfectly) covered, and the UI still surfaces the 0.2 gap via the numeric value shown alongside the band. |

**Distinction preserved (from slice 256 P0-256-1):** a genuine 0.0 score
_with_ an in-scope evaluated control (e.g. 0% pass rate) classifies as
**weak**, NOT **uncovered** — "covered but failing" is a real state
distinct from "no coverage at all." `uncovered` is reserved for the
no-contributing-path case.

**Confidence:** low. The 0.5 / 0.8 cut points and the four labels are the
single most-likely-to-be-tuned decision in the slice. The labels
(strong/partial/weak/uncovered) are a reasonable first vocabulary; an
auditor may prefer numeric-only, a 5-band scheme, or different words.

### D5 — UI surface: per-row confidence band on the control detail coverage table

The web app has no standalone requirement-detail page; the existing
detail view that consumes `/coverage` is the control detail page
(`/controls/[id]`), which already renders one row per mapped requirement
with its STRM type + edge strength (the slice 256 coverage table). The
band is rendered as a new **Confidence** column badge per row, derived
from the per-row `coverage` value with thresholds mirroring the backend
(`web/components/control/confidence-band.ts`). This is the literal AC-4
shape ("control/requirement detail view renders … the rolled-up
coverage_strength with a visible band") on the existing surface, with no
new page and no new charting dependency (a styled badge, per the
mechanics note). The requirement-side endpoint additive fields
(`coverage_strength` + `confidence_band`) ship with a BFF route + lib
type (`web/lib/api/requirement-coverage.ts`) so a future
requirement-detail page consumes them without backend work.

**Confidence:** high for the placement; the band labels themselves are D4.

### D6 — Defensive default to uncovered, never 500 / never over-report

Any error computing the tenant-evaluated state for a control (transient
eval/scope error), or an unwired Handler (engine/scope/fwScope nil on a
unit server), degrades that control's contribution to "no coverage"
rather than failing the whole `/coverage` read. The rollup is a display
value, not an audit-binding artifact (threat-model R), so degrading to a
_lower_ score (worst case: uncovered) is safe — it never over-reports
coverage. Mirrors slice 256's null-on-error posture.

**Confidence:** high.

---

## Revisit once in use

1. **Band cut points (0.5 / 0.8)** — the top of the list (D4, low
   confidence). Re-tune against real auditor expectations once a tenant
   has a populated control kit. A requirement at 0.79 reading "partial"
   vs 0.80 reading "strong" is a 1pp cliff an auditor may want softened.
2. **Band labels** — strong/partial/weak/uncovered. Revisit whether a
   5-band scheme, numeric-only display, or different vocabulary reads
   better to a non-technical board audience vs a technical auditor.
3. **MAX vs weighted blend (D1)** — once requirements with 3+ anchors at
   varied strengths exist in real data, re-check whether "best path"
   over-credits a requirement that has one strong path and several weak
   ones an auditor would want acknowledged.
4. **Freshness weighting (D3)** — add a distinct freshness _penalty_
   (vs the 30-day window cutoff) once a per-evidence-age signal is worth
   surfacing: "covered, evidence 28 days old" reading weaker than
   "covered, evidence 2 days old."
5. **`anchor_coverage` = MAX over controls on an anchor** — when a tenant
   has multiple controls on one anchor (e.g. two MFA controls), confirm
   "best control wins" is the right rule vs an average or a coverage-
   completeness measure.
6. **Requirement-detail page** — when one ships, render the
   requirement-level `coverage_strength` rollup (already exposed on the
   endpoint + BFF) as the headline number with the per-anchor breakdown,
   so the canvas §3.2 example is shown requirement-first, not just
   control-first.

---

## Confidence summary

| Decision                                                  | Confidence |
| --------------------------------------------------------- | ---------- |
| D1 rollup formula (best-satisfying-path / MAX)            | medium     |
| D2 anchor_coverage = effectiveness, strength applied late | high       |
| D3 no freshness weighting in v1                           | medium     |
| D4 band thresholds + labels                               | low        |
| D5 UI placement (per-row band on control detail)          | high       |
| D6 defensive default to uncovered                         | high       |
