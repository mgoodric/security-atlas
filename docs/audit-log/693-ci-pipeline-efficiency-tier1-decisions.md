# Slice 693 — CI pipeline efficiency + safety hardening (Tier 1) — decisions log

JUDGMENT slice. The build-time subjective calls (which audit findings are
"Tier 1" vs. deferred, the timeout-minutes values, the Codecov knobs, the
Dependabot grouping shape, and whether to introduce `actions/cache`) are
recorded here per the continuous-batch JUDGMENT convention; the maintainer
iterates post-deployment. This does NOT touch the product-runtime AI-assist
boundary (separate, constitutional).

Source: the 2026-06-11 pipeline-efficiency audit (three parallel investigations:
ci.yml structure, auxiliary workflows + merge-gate, and the PR bot-comment
census). Cross-references: slice 069 (four enforced test surfaces), slice 128
(SHA-pin discipline — `scripts/check-action-pins.sh`), slice 458
(`cache-path-guard`), slice 474 (main-canary masking fix), slice 353 Q-16
(no integration retry). Follow-on slices: 694–703.

- detection_tier_actual: manual_review
- detection_tier_target: manual_review

(The 360-minute-default runaway risk, the missing Codecov config, and the
ungrouped Dependabot ecosystems are configuration gaps a CI-config review
catches; none had surfaced as a production incident, so `actual == target ==
manual_review`. No bug was introduced or fixed in platform code.)

---

## D1 — What counts as "Tier 1" (the cut)

The audit produced ~13 recommendations across three risk bands. Tier 1 is the
subset where ALL of these hold: (a) additive (no behavior removed), (b)
independent of the others (can land/revert in isolation), (c) zero or near-zero
stability/security risk, (d) config-only (no platform code). That selects
exactly four: timeout-minutes, codecov.yml, Dependabot grouping, precommit
caching. Everything requiring an artifact-sharing refactor (695/696), a new
Docker build path (694), a scanner reschedule (700), a comment-logic change
(699), or a branch-protection semantics change (701) is deferred to its own
slice so it gets isolated review. The stub-collapse (701) is explicitly NOT
Tier 1: it changes what a green merge means and needs a merge-gate `needs:`
completeness audit first.

## D2 — timeout-minutes values: 20 default / 30 heavy

Chosen to bound the 6-hour default without risking a false trip on a legitimate
slow run. The slice-393 watermark already targets a ~20-minute trip-wire for the
integration tier, so 30 is a safe ceiling for the heaviest jobs (integration
shards, the three playwright/ui-honesty jobs, test-self-host-bundle, trivy-image,
main-canary). 20 is generous for build/lint/guard/stub jobs that finish in
seconds-to-low-minutes. The values are a ceiling, not a target — they only matter
when a job hangs. Applied via a deterministic script that inserts after each
`runs-on:` line (all 56 jobs are `ubuntu-latest`); verified every `runs-on` is
immediately followed by a `timeout-minutes` and the count is 56.

## D3 — Codecov: informational statuses, not a new gate

`require_changes: true` + `after_n_builds: 2` is the actual noise fix (no comment
on no-op PRs; no premature comment before both upload flags land). The statuses
are marked `informational` rather than removed so the coverage signal stays
visible in the PR checks UI. Deliberately NOT making Codecov merge-blocking: the
project's merge-blocking coverage gate is `cmd/scripts/coverage-gate` (the
ratchet in `coverage-thresholds.json`), and `fail_ci_if_error: false` is already
set on both upload steps. Adding a Codecov hard gate would create a second,
database-timing-dependent coverage gate — exactly the kind of flake the project
avoids. So Codecov stays advisory.

## D4 — Dependabot: group minor/patch, keep majors isolated

The catch-all groups use `update-types: [minor, patch]` so a breaking major still
gets its own reviewable PR (a grouped major that fails CI would otherwise block
every dep in the group). Security updates are unaffected — Dependabot's
security-update path is independent of version-update grouping. `gomod`/`npm`
keep their existing semantic groups (go-otel, next, etc.); the catch-all only
sweeps the remainder. The two pip directories and docker, previously fully
ungrouped, gain a single minor/patch group each. PR-limit values left unchanged
(the grouping itself is the volume reducer).

## D5 — Introducing `actions/cache` (first use in the repo)

The repo deliberately relied on setup-go/setup-node built-in caching and had no
`actions/cache` usage. The precommit hook-environment cache (`~/.cache/pre-commit`)
is the single biggest cost of that job and is NOT covered by the language-setup
caches, so `actions/cache` is the right tool. Pinned to a 40-char SHA
(`0057852bfaa89a56745cba8c7296529d2fc39830`, v4) with a `# v4` comment per the
slice-128 discipline; `check-action-pins.sh` enforces only SHA-pinning (no
allowlist), so the new action passes. `cache-path-guard` is unaffected — the
cache lives on the runner, nothing is committed to the tracked tree. The cache
key busts on `.pre-commit-config.yaml` changes (hook-rev bumps) with a
`restore-keys` fallback for warm reuse.

## D6 — Why these four do not need a CI-behavior regression test

Each change is declarative CI config validated by the pipeline itself on this very
PR: actionlint + check-yaml + prettier validate the workflow/YAML edits;
`actions-pin-check` validates the new SHA pin; the precommit job exercises its own
new cache step; Dependabot config is schema-validated by GitHub on push. There is
no platform code path to unit-test. The verification surface is "the PR's own CI
run is green", which is the correct tier for CI-config changes.
</content>
