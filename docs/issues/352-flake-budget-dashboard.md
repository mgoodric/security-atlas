# 352 — Flake budget formalization + dashboard

**Cluster:** Quality / observability
**Estimate:** 2d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Slice 333's QA strategy audit
(`docs/audits/333-qa-strategy-gap-analysis.md`) finding Q-15 and
AC-5: the project has no documented flake budget. Default policy is
"any flake blocks merge; investigate to root cause" — strong, caught
the chromedp regression (slice 340 / 341), also expensive (1-2d per
investigation × 4-5 per quarter = real maintainer cost).

This slice formalizes the flake budget proposed in slice 333's audit
report and ships a weekly dashboard so the budget is observable.

### Proposed budget (from slice 333 audit)

| Surface         | Target flake rate | Retry policy     | Investigation trigger         | Debt cap                                          |
| --------------- | ----------------- | ---------------- | ----------------------------- | ------------------------------------------------- |
| Go unit         | 0%                | None (hard fail) | 1 flake = investigation slice | Any flake blocks the surface                      |
| Go integration  | <0.5% per package | None (hard fail) | 2 flakes in 7d = slice        | 3+ flakes in 14d = surface freeze on new packages |
| Frontend vitest | ~0%               | None (hard fail) | 1 flake = investigation slice | Any flake blocks the surface                      |
| Playwright e2e  | <1% per spec      | 1 retry in CI    | 2 flakes in 7d = slice        | 5% surface-wide flake rate = no new specs land    |

### What ships

1. **Budget document.** `docs/flake-budget.md` formalizes the
   above. Versioned; updates require a slice.
2. **Flake counter.** A CI job that walks the last 7 days of CI runs
   (via GitHub API), counts test-level failures that did NOT recur
   on the next merged commit (== flake by definition), and emits a
   per-surface rate. Run weekly via cron, OR run on every merge to
   `main` and aggregate to a sidecar file.
3. **Dashboard.** A simple markdown file at
   `docs/flake-budget-dashboard.md` updated by the counter:
   columns surface · week · flake rate · target · status · top-3
   flaking tests.
4. **Trigger automation.** When a per-surface rate crosses the
   investigation threshold, the counter opens a GitHub issue
   labeled `flake-investigation` with the offending test names. The
   maintainer triages: either file a slice (slice 340 pattern) or
   close as resolved.
5. **Documentation.** CLAUDE.md "Testing discipline" gains a
   "Flake budget" subsection pointing at `docs/flake-budget.md`.

### Why this matters

Without a budget, "is this flake worth investigating?" is a per-incident
judgment. With a budget, it is a mechanical decision: rate crossed
threshold = investigation triggered. This converts an undefined
future maintenance load into a tracked, predictable one.

## Threat model

This slice ships a dashboard reporting test reliability. STRIDE pass:

- **I (information disclosure):** The dashboard names which tests
  flake most often — a roadmap to find tests that may be silently
  malfunctioning. **Mitigation:** the dashboard lives in the repo;
  access is the repo access-control surface. Same discipline as
  slice 333.
- Others: CLEAN.

## Acceptance criteria

- [ ] **AC-1.** `docs/flake-budget.md` documents the budget per
      slice 333's proposal.
- [ ] **AC-2.** Flake counter implemented as a workflow at
      `.github/workflows/flake-counter.yml` (or a script invoked by
      an existing job — implementer's call).
- [ ] **AC-3.** `docs/flake-budget-dashboard.md` is updated by the
      counter at least weekly.
- [ ] **AC-4.** Trigger automation files a `flake-investigation`
      labeled issue when rate crosses threshold.
- [ ] **AC-5.** Baseline measurement: run the counter against the
      last 90 days of CI to populate an initial dashboard. Document
      the baseline rates.
- [ ] **AC-6.** CLAUDE.md "Testing discipline" updated.
- [ ] **AC-7.** Cross-references slice 333 Q-15, slice 340 + 341.
- [ ] **AC-8.** `pre-commit run --files` passes.

## Anti-criteria

- **P0-1.** Does NOT lower the merge-block bar — flakes still block
  merge per slice 322. The budget is for AGGREGATE rate tracking,
  not per-incident allow-listing.
- **P0-2.** Does NOT auto-quarantine flaking tests. The trigger
  files an issue; a human decides whether to quarantine.
- **P0-3.** Does NOT add a flake retry policy to the Go integration
  tier without explicit decision (Q-16 in tactical round-1).

## Dependencies

- **#333** (QA strategy audit) — `merged`. Defines Q-15.
- **#340** (chromedp flake investigation) — `merged`. Canonical
  flake-investigation pattern.
- **#341** (chromedp wsurlreadtimeout fanout) — `merged`. The
  follow-up pattern.

## Notes for the implementing agent

The numerical thresholds in the budget table are the slice 333
audit's proposal, not gospel. Implement with the proposed numbers;
revisit after 1 quarter of operation. If the budget proves too
aggressive (too many false-positive investigations), loosen the
thresholds; if too lax (real flakes slip through), tighten.

The flake counter can start simple — a shell script that greps
`gh run list --json` output for test-failure patterns is fine for
v1. A proper telemetry pipeline is v2 if needed.
