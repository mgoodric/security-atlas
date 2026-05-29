# 345 — CI integration-job enrolment-discovery: decisions log

**Slice:** 345
**Type:** AFK
**Date:** 2026-05-29
**Branch:** `infra/345-ci-integration-enrolment-discovery`

This is an AFK-type slice; the build-time calls are recorded here rather
than blocked on human sign-off. The product runtime AI-assist boundary is
not touched.

---

## D1 — Shape: standalone shell script (option 1)

The slice doc offered three shapes: (1) a standalone shell script in CI,
(2) a Go meta-test, (3) an inline CI step. **Chose (1)**, matching the
slice's own recommendation and the `scripts/audit-rls.sh` /
`scripts/check-action-pins.sh` / `scripts/check-openapi-drift.sh`
precedent set.

- Rejected (2) Go meta-test: would require enrolling itself (a
  meta-problem), pulls a build-tag dependency, and is slower. A pure
  bash + grep check is sub-second and has no Go toolchain dependency, so
  it can run as its own lightweight CI job that doesn't wait on
  `setup-go`.
- Rejected (3) inline step: the check wants its own self-test
  (`audit-integration-enrolment_test.sh`) and a local-repro entry point
  (`just audit-integration-enrolment`); a script file gives both for
  free. An inline step cannot be unit-tested.

The CI job (`integration-enrolment-check`) mirrors `actions-pin-check`
exactly: unconditional (no `changes.code` gate — any PR that adds an
`integration_test.go` must be caught even if it touches no other code),
no stub-twin, blocking.

## D2 — The known-gaps allowlist + slice-387 spillover (the load-bearing call)

**The premise that "the list is current" is empirically false.** Building
the guard surfaced that **38 packages carry a `//go:build integration`
tag but are NOT enumerated in the `ci.yml` integration list** — their
integration tests (including `internal/api/oauth`'s 28 test functions and
`internal/exception`'s 22) have never run in CI. This is precisely the
I-1 gap class the slice exists to prevent, and it was independently
catalogued by slice 348's `excludes` audit
(`docs/audits/348-coverage-excludes-audit.md`, category (c) TEST_PRESENT,
which explicitly notes it is "a class of issue slice 345 is filed
against").

This creates a direct tension between three of the slice's own criteria:

- **AC-2** — guard must FAIL when a tagged package is unlisted.
- **AC-3** — guard must SUCCEED when every such package is listed.
- **AC-4 / P0-345-1** — must NOT retroactively enrol the forgotten
  packages (that is separate enrolment work).

A naive strict directory-diff guard fails on the current tree (38
unlisted), which both contradicts AC-3 and would block this PR's own CI.
Enrolling the 38 to make it pass would violate AC-4 / P0-345-1.

**Resolution: a documented, dated `KNOWN_UNENROLLED` allowlist** seeded
with exactly the 38 packages, baked into the script. The guard:

- FAILS on any tagged package that is neither listed nor waived (AC-2 —
  catches the 39th forgotten package).
- PASSES on the current tree (AC-3, and lets this PR's CI go green).
- Leaves the `ci.yml` package list untouched (AC-4 / P0-345-1 — no
  retroactive enrolment).

The allowlist is a **ratchet — shrink only** (same discipline as slice
069's coverage `excludes`). Draining the 38 is filed as **spillover
slice 387** (`docs/issues/387-integration-enrolment-backlog-drain.md`),
which enrols the packages in batches (security-critical first) and
removes each from the allowlist as it adds it to the list. A stale-waiver
hygiene check in the script (exit 2) prevents the allowlist from rotting:
a waiver for a package that no longer carries the tag fails loud.

This is the honest root-cause resolution: slice 345 builds the _guard_
that surfaces the gap (its job); slice 387 _drains_ the backlog (separate
work, per the slice's own anti-criteria and slice 348's design intent).

## D3 — Path normalization keys on the `internal/` segment

The three sets being compared (tagged dirs from `grep`, listed dirs from
`ci.yml`, the allowlist) must use one canonical path shape. Normalized
all tagged dirs to an `internal/<pkg>` suffix by stripping everything up
to and including the last `/internal/`. This makes the comparison robust
to (a) the real repo (paths under the repo root) and (b) the test
fixtures (absolute `/tmp/...` paths under a synthetic `internal/`), which
the `audit-rls.sh`-style `REPO_ROOT`-relative stripping did not handle.

## D4 — Blocking, not advisory

Made the CI job blocking (matches `actions-pin-check` / slice 140
`openapi-drift-check`), not informational (the slice-069/089/109/120/127
pattern). Rationale: the gap is **invisible by construction** — a
forgotten enrolment produces a GREEN build whose integration suite never
ran. An advisory job would let the regression re-accumulate silently,
which is exactly the failure the 17-slice retroactive trail documents. A
sub-second re-run is cheap; the silent-coverage-hole is not.

Note: this job is NOT yet added to `.github/branch-protection.json`
required-checks. Adding a new required check is an orchestrator/maintainer
action (it changes branch protection live state) and is deliberately left
out of this PR. The job runs and blocks the PR's own check list; promoting
it to a hard required-check on `main` can ride a follow-up branch-protection
reconcile.

## D5 — Self-test as the first CI step

The CI job runs `audit-integration-enrolment_test.sh` (synthetic
positive + negative fixtures) BEFORE the real-tree audit, so a regression
in the guard's own logic is caught even on a tree that happens to pass
the real audit. Mirrors `check-branch-protection-drift_test.sh` (slice 127) and slice 382's synthetic positive+negative precedent.

---

## Spillover filed

- **Slice 387** — `docs/issues/387-integration-enrolment-backlog-drain.md`
  — drain the 38-package enrolment backlog the guard surfaced. Status
  `ready`, depends on #345 (this slice) + #348 (merged).

## Anti-criteria compliance

- **P0-345-1** (no retroactive enrolment): honored — the 38 are waived,
  not enrolled; the `ci.yml` list is byte-for-byte unchanged.
- **P0-345-2** (no test-file modification): honored — only new files
  (`scripts/audit-integration-enrolment.sh`, its `_test.sh`); no existing
  `*_test.go` touched.
- **P0-345-3** (no CLAUDE.md / canvas): honored.
- **P0-345-4** (independently mergeable, not bundled with 346/347):
  honored — this PR touches only slice-345 surfaces.
