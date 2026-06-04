# 393 — Wire slice-353 QA-tactical scripts into CI

**Cluster:** Infra
**Estimate:** 0.5d
**Type:** MECHANICAL
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 353, captured per continuous-batch policy.

Slice 353 (QA strategy tactical round 1) shipped three QA-tooling scripts
with passing self-tests and `justfile` targets, but could NOT wire them
into `.github/workflows/ci.yml`: batch-166 reserved `ci.yml` to avoid a
merge collision (the batch directive explicitly instructed deferring any CI
change to a spillover slice — the same steering slice 369 → 391 used in
batch-164).

This slice adds the three CI steps so the guards run on every PR (the
exclude-justification lint as a hard failure) and on clean main runs (the
wall-clock watermark), completing slice 353 AC-2 / AC-3 / AC-4's CI portion.

## What ships

Three additions to `.github/workflows/ci.yml`:

1. **Q-5 — coverage-exclude justification lint (HARD FAIL).** A new step in
   the meta-lint family (mirror the `integration-enrolment-check` /
   `openapi-drift-check` self-contained shape), running the self-test then
   the guard:

   ```yaml
   - name: coverage-excludes (slice 353 Q-5 guard)
     run: bash scripts/check-coverage-excludes_test.sh && bash scripts/check-coverage-excludes.sh
   ```

   Exits non-zero (fails the check) when an exclude lacks a justification or
   an orphan justification exists.

2. **Q-6 — assertion-density (ADVISORY / informational).** An informational
   step (NOT merge-blocking — the script is advisory by design, exit 0 even
   with warnings) that runs the self-test then emits the density report,
   optionally posting it as an informational PR comment like the
   `audit-deps` job:

   ```yaml
   - name: assertion-density (slice 353 Q-6 advisory)
     run: bash scripts/assertion-density_test.sh && bash scripts/assertion-density.sh
   ```

3. **Q-8 — integration wall-clock watermark (clean-main only).** A step on
   the `Go · integration` job's clean-main path that records the job's
   wall-clock to `docs/integration-wallclock.tsv`. Use the integration job's
   own measured duration via `WALLCLOCK_SECONDS` (RECORD mode) rather than
   re-running the suite. It warns — does not fail — at the 20-min trigger.

## Acceptance criteria

- [ ] **AC-1.** `ci.yml` runs `scripts/check-coverage-excludes.sh` as a
      hard-failure step (with its self-test as the preceding step).
- [ ] **AC-2.** `ci.yml` runs `scripts/assertion-density.sh` as an
      informational (non-blocking) step.
- [ ] **AC-3.** `ci.yml` records the integration wall-clock watermark on
      clean-main runs via `scripts/measure-integration-wallclock.sh`
      (RECORD mode, the 20-min trigger informational).
- [ ] **AC-4.** Each step mirrors an existing precedent's shape
      (meta-lint job for Q-5; `audit-deps` informational job for Q-6).
- [ ] **AC-5.** `pre-commit run --all-files` passes; CI green on the PR.

## Dependencies

- **#353** (the scripts) — must merge first; this slice wires the scripts it created.
- Any batch-166 `ci.yml` owner — sequence after to avoid the merge collision
  slice 353 was steering around.

## Anti-criteria

- **P0-393-1.** Does NOT change any script's logic — CI wiring only.
- **P0-393-2.** Does NOT make the assertion-density step merge-blocking
  (advisory by design — slice 353 D7).
- **P0-393-3.** Does NOT make the wall-clock step merge-blocking (recorder,
  not a gate — slice 353 D6).
- **P0-393-4.** Does NOT auto-merge.

## Notes

Mechanical. The wall-clock step's cleanest shape feeds the integration job's
already-known duration into RECORD mode; confirm the job exposes its elapsed
time (GitHub provides step timing, or wrap the test invocation in a
`date +%s` bookend in the same job).
