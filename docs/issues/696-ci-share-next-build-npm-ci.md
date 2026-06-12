# 696 — Share the `.next` frontend build + standardize on `npm ci`

**Cluster:** CI / Infra
**Estimate:** M
**Type:** JUDGMENT
**Status:** `ready`
**Priority:** P2
**Spillover from:** slice 693 (pipeline-efficiency audit, Tier 2).

## Narrative

Two adjacent frontend-CI inefficiencies:

1. **`next build` runs ~4× per code PR.** `build-frontend`, `frontend-playwright`,
   `frontend-playwright-prod-build`, and `frontend-ui-honesty` each run a Next build; only
   `frontend-vitest`/`frontend-lint` correctly skip it (they need only `node_modules`). A
   Next build is the heaviest single frontend step (~60–150s). `build-frontend` and
   `frontend-playwright` produce the SAME standard `.next` output and could share it via an
   artifact; `frontend-ui-honesty` consumes the same build. The prod-build job uses
   `build:standalone` (different output mode) and keeps its own build.

2. **6 frontend jobs use `npm install`; only `npm-audit` uses `npm ci`.** `npm install` is
   non-deterministic and can mutate the lockfile. All CI frontend jobs should use `npm ci`
   (faster, lockfile-faithful, no resolver pass) — both a correctness and a minor speed win.

This is filed at M (not S) because `.next` artifacts can be 100s of MB; upload/download time
can erode the saving. The slice must BENCHMARK the artifact round-trip vs. a fresh build and
only adopt artifact-sharing where it actually wins; the `npm ci` standardization is
unconditionally adopted regardless.

## Acceptance criteria

- [ ] **AC-1.** Every CI frontend job uses `npm ci` (not `npm install`).
- [ ] **AC-2.** `build-frontend` uploads the standard `.next` build as an artifact.
- [ ] **AC-3.** `frontend-playwright` and `frontend-ui-honesty` download the `.next` artifact
      instead of rebuilding — ONLY if the round-trip benchmarks faster than a fresh build
      (AC-5); otherwise this AC is dropped with a recorded rationale.
- [ ] **AC-4.** `frontend-playwright-prod-build` keeps its own `build:standalone`.
- [ ] **AC-5.** PR body records the artifact-round-trip-vs-rebuild benchmark.
- [ ] **AC-6.** All frontend e2e/vitest/lint suites stay green; Node version stays pinned
      (artifact portability assumption).

## Anti-criteria

- Does NOT share the `build:standalone` output (it is a different build mode).
- Does NOT change any frontend test assertions.
- Does NOT adopt artifact-sharing blind to the size/round-trip cost.

## Dependencies

- Independent of 694/695.

## Notes

Source: slice 693 audit Finding 2.2.
</content>
