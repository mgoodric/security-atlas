# Slice 216 — Decisions log

- **Slice**: 216 — Audits mockup "Sample size" column stale
- **Type**: JUDGMENT
- **Spec**: `docs/issues/216-audits-mockup-sample-size-column-stale.md`
- **Parent**: surfaced by slice 204 audits-page audit (`docs/audit-log/204-page-audit-audits.md` finding #4)
- **Date**: 2026-05-23

## Decisions made

### D1. Path A (mockup-side drop) chosen over Path B (extend periodWire)

**Options considered:**

- **Path A (chosen).** Drop the `Sample size` column from `Plans/mockups/audits.html` — header + six per-row TD cells.
- **Path B (rejected for now).** Extend `audit_periods` schema with a denormalized `sample_size INT`, populate via a nightly job, ship `periodWire.sample_size`, render the column live.

**Chosen path: A.** Rationale, in order of weight:

1. **The mockup is the design-intent source-of-truth for v0 UX**, not a forward roadmap. Carrying a column the live page intentionally omits creates a permanent mockup-vs-live divergence that confuses every future per-page audit (exactly the failure mode that surfaced this slice). Aligning the mockup to the implemented contract closes the loop.
2. **Slice 102's `P0-A4` invariant ("NO invented columns — every column derived from periodWire")** is the operative constraint. Path B would re-open a frozen contract; Path A honors it.
3. **Product-value gap is unproven.** The mockup author put sample size at the period-list level on intuition ("glanceable big/small signal"), but the operator's actual list-page workflow is "which period am I auditing right now" and "which periods are frozen" — both already served by Name, Period, Status, Frozen. The list-page sample-size column is not a missing primary affordance; it's a nice-to-have, and the per-period detail view is the right home for it if/when the rollup lands.
4. **Path B is a ~6× larger lift** (migration + sqlc query + cron job composition + handler change + UI + integration test) for a column whose product value is unclaimed. Premature optimization of a denormalized rollup.
5. **Reversibility is symmetric.** If real operator usage later proves the column carries weight, Path B can ship as a clean spillover slice; the mockup can be updated back at that point. Choosing A first preserves both options; choosing B first commits us to maintaining the rollup forever.

**Anti-criteria honored:**

- **P0-216-1** (no hot-path computation) — N/A under Path A.
- **P0-216-2** (no post-frozen evidence pollution) — N/A under Path A.
- **P0-216-3** (does NOT remove the mockup file) — honored; in-place edit only.
- **P0-216-4** (paths are mutually exclusive — does NOT also extend the schema) — honored; zero schema/Go/sqlc/handler/UI changes in this slice.

**Confidence:** `high`. The decision pattern-matches cleanly to existing slice norms (mockup serves design-intent; live page enforces periodWire contract; rollups land as deferred jobs, not list-page hot paths). The reversibility argument plus the product-value-unproven argument plus the slice-102 invariant alignment all point the same way.

## Revisit once in use

Reasons to reconsider Path B and ship the column live:

1. **Operator workflow signal.** If, after the audits page has been driven against ≥2 real audit cycles, the operator reports they regularly need to glance at sample-size at the list level (not detail level) — e.g., when triaging "which historical period should I open first to find a comparable sample population for this new audit" — that's the trigger to ship Path B.
2. **Auditor workflow signal.** If an external auditor's first request when opening the page is "show me how big each period is", same trigger.
3. **Per-period detail page lands first.** Per the spec narrative, sample size is plausibly more useful on the detail view than the list view. When the per-period detail page (slice 184 follow-on family) ships with sample-size + sample-population rendering, re-evaluate whether the list page also needs the column or whether the detail-page surfacing is sufficient.
4. **Board-narrative integration.** If board-narrative v0 (slice 182 family) ends up wanting "audit periods this quarter and their sample sizes" as a templated section, the rollup becomes load-bearing for narrative input and Path B graduates from optional to required.

When any of (1)–(4) trip, file a new slice that:

- Adds `sample_size INT` to `audit_periods` (additive, reversible migration)
- Adds a nightly job at `internal/jobs/audit_period_sample_size` that respects `observed_at ≤ frozen_at` for frozen periods (invariant 10 — audit-period freezing)
- Extends `periodWire` to carry `sample_size` (always present, nullable until first job run)
- Adds the column back to BOTH the mockup AND the live page in the SAME slice (no re-divergence)

## Confidence

| Decision                              | Confidence |
| ------------------------------------- | ---------- |
| D1 (Path A — drop column from mockup) | high       |

## Files touched

- `Plans/mockups/audits.html` — removed `Sample size` `<th>` header (was line 169) and six per-row `<td>` cells (was lines 180, 189, 198, 207, 216, 225). Net delta: −7 lines.
- `docs/audit-log/216-decisions.md` — this file (new).

Per the slice's `Type: JUDGMENT` contract:

- `docs/issues/_STATUS.md` row flip happens via the reconcile PR after merge, NOT here.
- `CHANGELOG.md` is untouched — mockup-only change is not user-facing.
- No `Plans/canvas/*` edits — the canvas already documents the policy ("NO invented columns") that this slice operationalizes.
