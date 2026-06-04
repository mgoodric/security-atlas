# 216 — Audits mockup "Sample size" column stale (no backing data; decide: drop from mockup or extend periodWire)

**Cluster:** frontend
**Estimate:** 0.25d (mockup-only path) · 1.5d (extend periodWire path)
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 204 audit fleet (audits page), captured as
follow-up per continuous-batch policy.

The mockup at `Plans/mockups/audits.html` renders a `Sample size`
column in the audit-periods table (`Plans/mockups/audits.html` lines
169, 180, 189, 198, 207, 216, 225 — six values like `1,847 records`
/ `2,104 records`).

The live `/audits` page intentionally OMITS this column. Slice 102's
header comment is explicit:

> "P0-A4: NO invented columns — every column is derived from
> periodWire (name, framework_version_id, period_start, period_end,
> status, frozen_at, frozen_by, created_by). Mockup shows a 'Sample
> size' column but periodWire does NOT carry it — we OMIT the
> column rather than invent."

So the gap is real and pre-recognized. It needs resolution because
mockup-vs-live divergence accumulates technical debt:

- Path A (cheap). Update the mockup to drop `Sample size`. Aligns
  truth-of-record. ~0.25d.
- Path B (extends product). Add a `sample_size` field to periodWire
  - the underlying `audit_periods` row's denormalized rollup (count
    of `evidence_records` with `observed_at` between `period_start`
    and `period_end`, tenant-scoped). Then add the column to the live
    table. ~1.5d (migration + handler + sqlc + UI).

The product-value question is: does the operator need sample size
at the period-list level, or only at the period-detail level? The
mockup author put it on the list — suggesting it's a glanceable
"is this period big or small" signal. The slice-102 author chose
omission to honor P0-A4 (no invented data).

This slice asks the maintainer to resolve via JUDGMENT call: A or B.
Default recommendation is B with sample size computed as a deferred
nightly job (not live) so the rollup doesn't add hot-path
computation per request — but acknowledge B is the bigger lift.

## Threat model

**Verdict.** **no-mitigations-needed.** Path A is doc-only. Path B
reads existing evidence-records data; the count rollup is tenant-
RLS-scoped automatically.

## Acceptance criteria (path A — mockup-only)

- **AC-A1.** `Plans/mockups/audits.html` no longer shows the
  `Sample size` column (header removed, six TD cells removed).
- **AC-A2.** A short paragraph added to
  `docs/audit-log/204-page-audit-audits.md` documenting the
  decision and pointing to this slice.

## Acceptance criteria (path B — extend periodWire)

- **AC-B1.** `audit_periods` schema extended with `sample_size INT`
  populated by a nightly job (`internal/jobs/audit_period_sample_size`).
- **AC-B2.** `periodWire` JSON shape carries `sample_size` (always
  present, `null` until the nightly job has run).
- **AC-B3.** `/audits` table renders a `Sample size` column matching
  the mockup format: mono, "`<n>` records" or em-dash if null.
- **AC-B4.** sqlc query for the nightly job is COUNT-only against
  `evidence_records` with `observed_at` in the period's range.
- **AC-B5.** Integration test confirms the rollup matches a seeded
  evidence-records count.

## Constitutional invariants honored

- **Invariant 2 (separation of ingestion + evaluation).** The
  rollup is a read-model snapshot — it does NOT write to the
  evidence ledger; just queries it.
- **Invariant 6 (tenant isolation).** Per-tenant rollup; RLS-scoped
  COUNT.
- **Invariant 10 (audit-period freezing).** For a frozen period,
  the rollup MUST use `observed_at ≤ frozen_at` (not "current
  evidence count"). The COUNT query gates on the frozen boundary.

## Canvas references

- `Plans/mockups/audits.html` lines 162-228 (the table block)
- `Plans/canvas/08-audit-workflow.md` §8.4 — audit-period freezing
- `Plans/canvas/04-evidence-engine.md` — the evidence-records source

## Dependencies

- **#204** — UI parity audit (surfacing parent)
- **#102** — audits page (the consumer)
- **#048** — audit-periods backend (the periodWire owner)

## Anti-criteria (P0 — block merge)

- **P0-216-1.** Does NOT compute `sample_size` on the hot path. If
  path B, the field is rollup-computed by a scheduled job, not on
  every list-page render.
- **P0-216-2.** Does NOT use post-frozen evidence in the count for
  frozen periods. The COUNT respects `observed_at ≤ frozen_at`.
- **P0-216-3.** Does NOT remove the mockup file. Path A is an in-
  place edit to the existing mockup; the file stays.
- **P0-216-4.** If path A is chosen, does NOT also extend the
  schema. The two paths are mutually exclusive.

## Skill mix (3-5)

1. (path A) HTML editing
2. (path B) sqlc + database-designer for the nightly-job query
3. (path B) Go cron job composition
4. (path B) Playwright e2e assertion that the column renders the
   expected format
5. JUDGMENT decision documented per slice convention
