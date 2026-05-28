# Flake budget — security-atlas

> Slice 352 — formalization of the flake budget proposed in slice 333's
> QA strategy audit (finding **Q-15**). Companion dashboard at
> [`docs/flake-budget-dashboard.md`](flake-budget-dashboard.md).

---

## What this is

A **flake budget** is the per-surface watermark above which the project
treats accumulating flakes as a structural problem worth a dedicated
investigation slice — rather than triaging each incident individually.

The default discipline (pre-slice-352) was: **any flake blocks merge;
investigate to root cause.** That discipline is strong (it caught the
chromedp regression — slices 340 + 341) and remains the merge-time
contract. This document does **not** weaken it. What it adds is an
**aggregate-rate signal**: when a surface's measured flake rate
crosses the per-surface trigger, the counter files a
`flake-investigation` issue automatically. The maintainer triages
(file a slice, or close as resolved).

In other words: per-incident discipline stays unchanged; aggregate
observability is new.

## The budget

| Surface         | Target flake rate | Retry policy     | Investigation trigger         | Debt cap                                           |
| --------------- | ----------------- | ---------------- | ----------------------------- | -------------------------------------------------- |
| Go unit         | 0%                | None (hard fail) | 1 flake = investigation slice | Any flake blocks the surface                       |
| Go integration  | <0.5% per package | None (hard fail) | 2 flakes in 7 d = slice       | 3+ flakes in 14 d = surface freeze on new packages |
| Frontend vitest | ~0%               | None (hard fail) | 1 flake = investigation slice | Any flake blocks the surface                       |
| Playwright e2e  | <1% per spec      | 1 retry in CI    | 2 flakes in 7 d = slice       | 5% surface-wide flake rate = no new specs land     |

**Numbers are slice 333's proposal**, not gospel. Revisit after one
quarter of operation: if too aggressive (false-positive investigations
dominate), loosen; if too lax (real flakes slip through the aggregate),
tighten. The revisit is itself a slice — the budget table edits are
versioned, not silently tuned.

## Definition of "flake"

A test failure where:

- The job that failed was on a known surface (per the table above), AND
- A re-run of the same `head_sha` (no code change) on a later attempt
  produced `success` for that same job.

That is the **v1 signal** the counter uses, because it is the
unambiguous one — same SHA, same job, success-after-failure on re-run,
no code in between. The counter writeup at
[`scripts/flake-counter.sh`](../scripts/flake-counter.sh) implements
the rule mechanically against the GitHub Actions API.

A weaker secondary signal — failure on commit A followed by success on
commit A+1 with no test code change — is **not** counted as a flake by
v1. It conflates flake with fix-forward. Tightening this is a v2
question once we have a quarter of v1 data to compare against.

## Surface-to-CI-job mapping

The counter classifies a failed job into a surface by exact name match:

| Surface         | CI job name (from `.github/workflows/ci.yml`) |
| --------------- | --------------------------------------------- |
| Go unit         | `Go · build + test`                           |
| Go integration  | `Go · integration (Postgres RLS)`             |
| Frontend vitest | `Frontend · vitest`                           |
| Playwright e2e  | `Frontend · Playwright e2e`                   |

Failures in other jobs (lint, sqlc-diff, govulncheck, etc.) are tracked
in the dashboard's appendix but do not count against a surface budget.

## Cadence

- **Weekly cron.** `Monday 09:00 UTC` (matches the existing
  `edge-image-prune` cadence). Walks the last 7 days, refreshes
  [`docs/flake-budget-dashboard.md`](flake-budget-dashboard.md),
  opens issues if trigger thresholds were crossed.
- **`workflow_dispatch`.** Maintainer can run an ad-hoc recompute or a
  90-day baseline rebuild via the GitHub UI / `gh workflow run`.

## When the trigger fires

The counter opens a GitHub issue:

- **Title:** `flake-investigation: <surface> — N flakes in <window>`
- **Label:** `flake-investigation`
- **Body:** the offending test names + per-incident links to the failed
  runs + the dashboard's current row for the surface

The maintainer triages: file a slice (slice 340 pattern) or close as
resolved (e.g., the failing test was already removed in a follow-on
commit). The counter **does not auto-quarantine**, **does not auto-skip**,
and **does not modify product code**. It informs; humans decide.

The counter is idempotent on the issue side: if an open
`flake-investigation` issue already exists for the same surface within
the trigger window, the counter does NOT file a duplicate — it appends
a comment on the existing issue.

## Cross-references

- **Slice 333 (QA strategy audit) — finding Q-15.**
  [`docs/audits/333-qa-strategy-gap-analysis.md`](audits/333-qa-strategy-gap-analysis.md)
  proposed this budget table verbatim.
- **Slice 340 (chromedp `TestRender_ProducesRealPDF` flake
  investigation).**
  [`docs/audit-log/340-chromedp-flake-decisions.md`](audit-log/340-chromedp-flake-decisions.md)
  is the canonical example of what a flake-investigation slice looks
  like once filed. The 5 consecutive failures across slices 312/315/320
  were a clear watermark-crossing event under the budget shape
  formalized here.
- **Slice 341 (chromedp `WSURLReadTimeout` fan-out).**
  [`docs/audit-log/341-chromedp-fanout-decisions.md`](audit-log/341-chromedp-fanout-decisions.md)
  is the follow-up pattern — a single investigation that ripples to all
  packages exposed to the same root cause.
- **Slice 346 (CI yaml integration-job history extraction).**
  Slice 346's docs-only PR #788 was blocked by a flake of
  `TestRun_FiresInlineSweepAndExitsOnCancel` in
  `internal/metrics/scheduler/integration_test.go:260` — a timing-
  sensitive goroutine race. That incident is the canonical
  "is this worth investigating?" moment that motivated formalizing
  the budget in this slice. See the baseline section of the dashboard
  for whether the test appears in the 90-day window.
- **Slice 069 (test-pyramid four-surface gate).** The flake budget is
  scoped to the four surfaces defined there. New surfaces added in
  future slices must add a row to the budget table at the same time.

## Anti-criteria (what this budget is NOT)

- **Not a per-incident allow-list.** Every flake still blocks the merge
  it occurs on. The budget is for the aggregate-rate signal; the
  merge-block bar is unchanged from slice 322.
- **Not auto-quarantine.** The counter NEVER `t.Skip`s, NEVER
  comments out a test, NEVER adds a `test.skip` to a Playwright spec.
  It files an issue and stops.
- **Not a flake retry policy for Go integration.** Whether the Go
  integration tier should gain `-retry 1` (matching the Playwright
  surface) is Q-16 in slice 333's audit and is **out of scope** for
  this slice. The current Go integration retry policy is "none" and
  this document does not change it.

## Versioning

This file is part of the slice surface — edits to the budget table
above (rates, trigger thresholds, debt caps) require a slice. The
dashboard at [`docs/flake-budget-dashboard.md`](flake-budget-dashboard.md)
is regenerated by the counter and is NOT slice-gated; it is a derived
artifact.
