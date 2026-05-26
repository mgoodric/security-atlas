# 310 — Coverage lift — `internal/api/soc2import` to 70%+

**Cluster:** Quality
**Estimate:** 1-2d (medium package, ~23% start)
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during the post-batch-122 audit. `internal/api/soc2import`
sits at floor `23` in `cmd/scripts/coverage-thresholds.json`.

This is the HTTP API for SOC 2 framework import — likely consumes the
slice-006 SCF importer + slice-058 (or similar) SOC 2 catalog importer
to instantiate a SOC 2 framework for a tenant. The package is HTTP-
handler shaped and likely has an integration_test.go that's not yet
enrolled in CI's tests-integration list — exactly the pattern slices
283 / 284 / 287 / 290 / 291 / 297 closed.

**Disposition:** unit-add (likely also "enroll in CI integration list")

## What ships in this slice

1. **Enroll `./internal/api/soc2import/...`** in
   `.github/workflows/ci.yml`'s `tests-integration` `-coverpkg` list
   IF an `integration_test.go` exists and isn't enrolled. (Check
   first — if no integration tests exist, the lift is unit-only.)
2. **New `handlers_test.go` or equivalent** under
   `internal/api/soc2import/` covering pre-DB branches + helpers
   (auth gate, request parsing, response shape).
3. **Floor ratchet** in `cmd/scripts/coverage-thresholds.json` from
   23 to `floor(measured - 2pp)`.

## Acceptance criteria

- [ ] **AC-1.** Merged coverage of `internal/api/soc2import` lifts
      to ≥ 70%.
- [ ] **AC-2.** Tests exercise real branches with real assertions.
- [ ] **AC-3.** File header names load-bearing functions + branches.
- [ ] **AC-4.** Floor ratchets to `floor(measured - 2pp)`.

## Constitutional invariants honored

- Testing discipline (CLAUDE.md): floor + tests in same PR.
- Slice 069 ratchet methodology.
- Invariant 1 (one control, N framework satisfactions): tests verify
  the SOC 2 import goes through SCF anchors, not direct
  framework → requirement mapping.
- Invariant 6 (RLS isolation): cross-tenant tests if the package owns
  tenant-scoped writes.

## Dependencies

- None.

## Anti-criteria (P0 — block merge)

- **P0-310-1.** Does NOT raise the floor without tests.
- **P0-310-2.** Does NOT lower any existing floor.
- **P0-310-3.** Does NOT modify `_STATUS.md` from inside the slice.
- **P0-310-4.** Does NOT bypass RLS in test fixtures — use the
  established `atlas_app` role + `app.current_tenant` GUC pattern.

## Skill mix

- Slice 291 (`internal/api/controls`) exemplar for HTTP handler unit
  - integration tests
- Slice 293 (`internal/api/metrics`) for handlers + helpers pattern
- Slice 006 (SCF importer) for framework-instantiation context

## Notes for the implementing agent

Read `internal/api/soc2import/*.go` first to understand the surface.
If there's a pre-existing `integration_test.go` that's not in CI's
list, enrolling it should be the highest-leverage move (mirrors slices
283 / 284 / 287 / 290 / 291 / 297 / 288 — all lifted via CI enrollment
of pre-existing suites).
