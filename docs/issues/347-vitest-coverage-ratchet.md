# 347 — vitest coverage ratchet

**Cluster:** frontend
**Estimate:** 1d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

The Go side of security-atlas enforces a monotonic coverage ratchet via
`cmd/scripts/coverage-gate` and `cmd/scripts/coverage-thresholds.json`
(slice 069 + the 17-slice retroactive enrolment trail). The TS side
measures coverage (vitest's `coverage-summary.json` is uploaded as a CI
artifact) but **nothing consumes the measurement** — there is no
ratchet.

The asymmetry is finding V-1 of slice 334's framework audit and the
load-bearing TS-side gap in the project's four-surface testing
discipline (CLAUDE.md). Slice 069's deferred-follow-up "raise the bar"
promise has been open for ~250 slices.

**The fix.** Wire a vitest coverage ratchet using either:

1. **Per-file thresholds in `vitest.config.ts`.** vitest's
   `coverage.thresholds` config supports per-file maps. Hand-curated;
   matches the Go-side `coverage-thresholds.json` shape; works inside
   the existing CI job (no new infra).
2. **A `cmd/scripts/coverage-gate-ts/` companion.** Mirrors the Go
   shape: a standalone gate that reads the `coverage-summary.json` and
   the threshold map; fails CI on regression. More work; cleaner
   separation between vitest's own thresholds (which can break the run)
   and the gate (which validates the artifact).
3. **vitest `coverage.thresholds.autoUpdate`.** vitest can write the
   ratchet floor back into the config on green. This is the lowest-toil
   path; the threshold-file becomes the canonical source.

Recommendation: option (1) for the first round (matches Go-side mental
model), then evaluate option (3) for autoamation. Option (2) is overkill
if the vitest built-in is sufficient.

**Floor methodology.** Match the Go-side rule: each floor = `max(0,
floor(measured - 2pp))`. Read the current coverage summary, populate
the map, commit. The ratchet starts at the current truth, never above.

**Why now:** slice 334 audit; slice 069's open promise; the asymmetry
itself signals that the TS surface is under-enforced relative to its
risk weight.

**Trigger:** Surfaced 2026-05-27 by slice 334 framework audit (finding V-1).

## Threat model

Frontend / CI slice with no auth or data surface. STRIDE pass: CLEAN
across all categories. The ratchet is a gate on regression, not a
runtime surface.

## Acceptance criteria

- [ ] **AC-1.** A vitest coverage ratchet is wired and enforces on
      every PR.
- [ ] **AC-2.** Floors are seeded from the current measured coverage
      using the same `max(0, floor(measured - 2pp))` rule slice 069
      uses for the Go ratchet.
- [ ] **AC-3.** The ratchet fails red on any per-file (or per-directory)
      coverage regression below floor.
- [ ] **AC-4.** The ratchet is monotonic: a follow-up slice that
      lifts a floor MUST write the additional tests in the same PR
      (per slice 069's `$how_to_raise` rule).
- [ ] **AC-5.** README or `web/testing.md` documents the rule and the
      lift-procedure.
- [ ] **AC-6.** `pre-commit run --all-files` passes.

## Constitutional invariants honored

- **Test-First Imperative (Article III).** A ratchet enforces that test
  coverage cannot regress — the negative-space enforcement of TDD.
- **Integration-First Testing (Article IX).** Coverage measurement
  must include the BFF route handlers (which test the real integration
  surface between Next.js and atlas).

## Canvas references

- `Plans/canvas/09-tech-stack.md` — testing discipline
- `CLAUDE.md` "Testing discipline (four enforced surfaces)" — the
  TS-side ratchet is the open promise

## Dependencies

- **#069** (testing discipline) — `merged`. The Go-side ratchet that
  this slice mirrors.

## Anti-criteria (P0 — block merge)

- **P0-347-1.** Does NOT lower any floor (the ratchet is monotonic ↑).
- **P0-347-2.** Does NOT modify production code; only test
  config + threshold data.
- **P0-347-3.** Does NOT seed floors above the measured value (slice
  069 P0-A4 mirror).
- **P0-347-4.** Does NOT bundle with slice 345 or 346.
- **P0-347-5.** Does NOT touch CLAUDE.md or canvas.

## Skill mix

- vitest config authoring
- Coverage analysis (reading `coverage-summary.json`)
- TS / JSON tooling

## Notes for the implementing agent

The current vitest job runs `npm run test:coverage` (see
`.github/workflows/ci.yml` `frontend-vitest` block). The
`coverage-summary.json` artifact is already uploaded; the data exists.
This slice's job is to read it, populate a thresholds map, and wire
vitest's built-in `coverage.thresholds` enforcement.

The decision between per-file vs per-directory thresholds is the
load-bearing call. Per-directory matches the Go-side mental model (one
floor per package). Per-file is more granular but explodes the map
size with ~80+ test files. Recommendation: per-directory under
`lib/**`, `app/api/**`, etc., matching the include-array shape in
`vitest.config.ts`.

The Go side's `coverage-thresholds.json` is a useful read for the
mental model: see `cmd/scripts/coverage-thresholds.json` and the
`$methodology` / `$how_to_raise` keys at the top.
