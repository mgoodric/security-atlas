# 345 — CI integration-job enrolment-discovery primitive

**Cluster:** infra
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`

## Narrative

The Go integration job in `.github/workflows/ci.yml` enrols packages by
explicit listing — currently 47 `./internal/<pkg>/...` entries on lines
515-568. A package that ships an `integration_test.go` but is not added
to that list silently runs no integration tests in CI; its coverage is
unit-only and any RLS / real-services bug it would catch goes unnoticed.

The pattern's cost is visible in retroactive enrolment slices: 279, 283,
284, 287, 288, 290, 293, 294, 295, 297, 310, 313, 315, 317, 318, 319,
320 each enrolled a package whose `integration_test.go` shipped earlier
and was forgotten. The 17-slice retroactive trail is the load-bearing
evidence — finding I-1 of slice 334's framework audit.

**The fix.** A discovery primitive that fails CI when a package
contains a `//go:build integration` build tag but is not in the
integration-job package list. Shell script + `grep -rl '//go:build
integration'` + diff against the yaml's enumerated list.

Shape options (pick one in implementation):

1. **Shell script in CI.** `scripts/audit-integration-enrolment.sh`
   greps the repo, parses the yaml's enumerated list, exits non-zero on
   diff. Same shape as `scripts/audit-rls.sh` (slice 033). Cheap.
2. **Go meta-test.** A test file in `cmd/scripts/` that asserts every
   package with the build tag is named. Plays nicely with `go test`.
   Slower; more dependencies.
3. **CI step in the integration job.** Inline `grep -rl` + `diff`. No
   new file; one job-step. Same constraint surface as (1) but lives
   inside the workflow.

Recommendation: option (1), matching the `audit-rls.sh` precedent.

**Why now:** slice 334's framework audit named this as the load-bearing
sustainability concern for the integration tier. The 17-slice
retroactive trail is the empirical cost of not having it.

**Trigger:** Surfaced 2026-05-27 by slice 334 framework audit (finding I-1).

## Threat model

Infra slice with no auth / data / network surface. STRIDE pass:

- **S/T/I/D/E:** CLEAN.
- **R:** the gate is enforced in CI; a contributor who removes the
  script edit + the matching list entry passes silently. Mitigation:
  treat removal of the script as a load-bearing CODEOWNERS line.

## Acceptance criteria

- [ ] **AC-1.** A discovery primitive (script or step) lives in the
      repo and runs in CI on every PR.
- [ ] **AC-2.** The primitive fails when a package contains a
      `//go:build integration` build tag and is NOT in the integration
      job's package list.
- [ ] **AC-3.** The primitive succeeds when every such package is
      listed.
- [ ] **AC-4.** The integration job's package list is unchanged by this
      slice (the discovery primitive validates current state; it does
      not retroactively enrol).
- [ ] **AC-5.** A test case demonstrates AC-2 by temporarily adding a
      no-op `internal/dummytest/foo_integration_test.go` and confirming
      red CI; that file is then removed before merge.
- [ ] **AC-6.** README or `CONTRIBUTING.md` documents the rule: "ship
      `integration_test.go`, also add to `ci.yml` integration list".
- [ ] **AC-7.** `pre-commit run --all-files` passes.

## Constitutional invariants honored

- **Integration-First Testing (Article IX).** This slice closes the gap
  that lets integration tests ship without running, which IS a hole in
  Article IX enforcement.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — testing discipline
- `CLAUDE.md` "Testing discipline (four enforced surfaces)" — the
  framework gate this slice protects

## Dependencies

- **#069** (testing discipline) — `merged`. The framework gate this
  slice patches.
- **#033** (RLS audit script) — `merged`. The precedent shape for
  option (1).

## Anti-criteria (P0 — block merge)

- **P0-345-1.** Does NOT retroactively enrol forgotten packages
  (separate slice if any surface).
- **P0-345-2.** Does NOT modify any test file.
- **P0-345-3.** Does NOT touch CLAUDE.md or canvas.
- **P0-345-4.** Does NOT bundle with slice 346 or 347 — each is
  independently mergeable.

## Skill mix

- Standard read/grep — yaml parsing + build-tag enumeration
- Shell script authoring (matching `audit-rls.sh` style)
- CI yaml edit

## Notes for the implementing agent

Read `scripts/audit-rls.sh` first — that script is the precedent shape
for this slice. The integration-job package list is at
`.github/workflows/ci.yml` lines 515-568.

The list is enumerated as `./internal/X/...` paths; the build tag check
runs on directory globbing. Translation: `grep -rl '//go:build
integration' internal/ | xargs dirname | sort -u` produces the set of
directories with integration tests; comparison against the parsed yaml
list is the gate.
