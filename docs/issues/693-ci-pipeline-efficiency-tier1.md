# 693 — CI pipeline efficiency + safety hardening (Tier 1)

**Cluster:** CI / Infra
**Estimate:** S (config-only; no platform code change)
**Type:** JUDGMENT
**Status:** `in-review`
**Priority:** P2

## Narrative

A pipeline-efficiency audit (2026-06-11) of `.github/workflows/ci.yml` (3426 lines,
56 jobs) and the auxiliary workflows surfaced a set of efficiency and safety gaps. This
slice lands the **Tier 1** subset: the four changes that are independent, additive, and
carry zero or near-zero stability/security risk. The higher-risk and higher-effort items
are filed as separate slices (694–703) so each gets its own review.

The audit's headline was that the pipeline is already well-engineered — concurrency is
correct, the slice-474 main canary fix is in place, security scanners run as **status
checks not comments**, and every bot comment is sticky (no per-run spam). Tier 1 is
therefore hardening + de-noising, not a rearchitecture.

The four changes:

1. **Runaway-job ceiling.** No job carried `timeout-minutes`, so each inherited GitHub's
   **360-minute** default. A hung Playwright/integration job (real servers: atlas + web +
   Postgres + NATS + MinIO, with `for i in seq 1 30` readiness loops that fall through
   silently) could burn up to 6 runner-hours before GitHub killed it. Every job now has a
   ceiling: 20 min default, 30 min for the eight heavy jobs (integration-shard,
   tests-integration, the three playwright/ui-honesty jobs, test-self-host-bundle,
   trivy-image, tests-integration-main-canary). Sized generously (~2× expected) so a
   legitimate slow run never trips — purely protective.

2. **Codecov noise.** No `codecov.yml` existed, so Codecov ran on defaults: a PR comment
   on every PR, and `project`/`patch` statuses that could fire before BOTH upload flags
   (`unittests` + `integration`) arrived. The new `codecov.yml` sets `after_n_builds: 2`,
   `require_changes: true`, and marks the statuses `informational`. The merged-coverage
   merge gate is enforced by `cmd/scripts/coverage-gate` (CI job `Go · build + test`), NOT
   by Codecov — so the change removes zero enforcement.

3. **Dependabot batching.** gomod/npm/github-actions had partial grouping but the two pip
   directories and docker were fully ungrouped — one PR per dep, each triggering the full
   CI matrix (the most expensive path in the repo). Catch-all `minor`/`patch` groups added
   to gomod, npm, both pip dirs, and docker. Majors stay ungrouped (own reviewable PR);
   Dependabot security updates fire independently of version-update grouping.

4. **pre-commit job caching.** The `precommit` job (runs on every PR, including docs-only)
   set up Go + Python + Node with no cache and re-installed pre-commit hook environments
   cold each run. It now sets `cache: true` / `cache: "npm"` on the Go/Node setup and
   caches `~/.cache/pre-commit` (the dominant cost) keyed on the config hash.

## Acceptance criteria

- [ ] **AC-1.** Every job in `.github/workflows/ci.yml` has a `timeout-minutes` (verify:
      `grep -c timeout-minutes` == job count). Heavy jobs = 30, rest = 20.
- [ ] **AC-2.** Root `codecov.yml` exists with `after_n_builds: 2`, `require_changes: true`,
      and `informational: true` on both `project` and `patch` status defaults.
- [ ] **AC-3.** `codecov.yml` does NOT introduce a hard Codecov status gate (the
      `coverage-gate` script remains the only merge-blocking coverage check).
- [ ] **AC-4.** `.github/dependabot.yml` has a catch-all minor/patch group on each of:
      gomod, npm, pip (`/`), pip (`/oscal-bridge`), docker. Majors remain ungrouped.
- [ ] **AC-5.** The `precommit` job sets `cache: true` (setup-go), `cache: "npm"`
      (setup-node), and an `actions/cache` step for `~/.cache/pre-commit`.
- [ ] **AC-6.** The new `actions/cache` reference is SHA-pinned (`actions-pin-check` /
      `scripts/check-action-pins.sh` passes).
- [ ] **AC-7.** `pre-commit run --all-files` passes locally (prettier on the new/edited
      YAML, actionlint on ci.yml, the cache-path-guard + check-yaml hooks).
- [ ] **AC-8.** `CHANGELOG.md` Unreleased gains the slice-693 bullet.

## Anti-criteria

- Does NOT remove or weaken any security scanner (CodeQL, govulncheck, npm-audit, Trivy,
  GitGuardian, actions-pin-check all unchanged).
- Does NOT add `-retry` to the integration tier (Q-16: no retry, investigate every flake).
- Does NOT lift the `-p 1` per-shard serialization or the per-shard separate-Postgres
  isolation (deliberate invariants).
- Does NOT touch the `cancel-in-progress: false` main-canary concurrency (slice-474 fix).
- Does NOT collapse the `-stub` jobs or change branch-protection semantics — that is the
  higher-risk slice 701, filed separately.

## Dependencies

- None. All four changes are independent and additive.

## Notes

Decisions log: `docs/audit-log/693-ci-pipeline-efficiency-tier1-decisions.md`.
Follow-on slices from the same audit: 694 (Docker layer cache on trivy-image), 695 (share
prebuilt atlas binary), 696 (share `.next` build + standardize `npm ci`), 697 (cache
uv/pip), 698 (de-dup precommit language hooks), 699 (PR-scope the advisory bot comments),
700 (move Trivy to nightly main schedule), 701 (collapse stub jobs behind a promoted
merge-gate), 702 (container-publish edge-build efficiency), 703 (main-canary single-leg on
docs-only pushes).
</content>
