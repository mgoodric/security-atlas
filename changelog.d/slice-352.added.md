- **feat(quality):** slice 352 — flake-budget formalization + dashboard.
  Closes slice 333 audit finding Q-15 (no documented flake budget).
  New canonical budget document at `docs/flake-budget.md` formalizing
  per-surface targets (Go unit 0%, Go integration <0.5%/pkg, vitest ~0%,
  Playwright e2e <1%/spec), retry policies, investigation triggers, and
  debt caps verbatim from the slice 333 proposal. New
  `scripts/flake-counter.sh` walks the GitHub Actions REST API for
  re-run-cleared-on-same-SHA flake events per surface, chunked into
  7-day API queries to work around the 1000-result endpoint cap.
  `scripts/flake-counter_test.sh` covers helper logic with 17
  assertions. New `.github/workflows/flake-counter.yml` runs every
  Monday 09:00 UTC (matching `edge-image-prune` cadence) plus
  `workflow_dispatch` for ad-hoc / baseline rebuilds; commits the
  regenerated `docs/flake-budget-dashboard.md` back to main with the
  bot identity and files / appends to `flake-investigation`-labeled
  issues when a surface crosses its trigger threshold. 90-day baseline
  measured at slice land: 0 flakes across 2003 attempts on each of the
  four surfaces (all green) — confirming the v1 narrow definition is
  silent on the existing slice 340 / 341 chromedp pattern (those were
  fail-on-different-SHAs, not re-run-cleared-on-same-SHA), with the
  data documented openly in
  `docs/audit-log/352-flake-budget-dashboard-decisions.md` D4. The
  budget does NOT lower the merge-block bar — every flake still blocks
  the merge it occurs on; the budget is for aggregate-rate tracking.
  CLAUDE.md "Testing discipline" gains a "Flake budget" subsection
  pointing at the new artifacts.
