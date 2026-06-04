# 311 — Coverage lift — `internal/auth/bearer` to 70%+

**Cluster:** Quality
**Estimate:** 0.5d (small package, ~68% start — only ~2pp needed)
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during the post-batch-122 audit. `internal/auth/bearer` sits
at floor `68` in `cmd/scripts/coverage-thresholds.json` — the smallest
gap-to-70% in the round.

This is the bearer-token validation package (likely slice 034's
opaque-bearer middleware, retired by slice 197 in favor of JWT). The
package may be near-deprecated; check whether it's still imported from
the live code path. If it's effectively dead code, file a spillover
slice to delete it instead of lifting coverage.

**Disposition:** unit-add OR delete-if-dead

## What ships in this slice

EITHER (A) the lift:

1. New unit tests covering the remaining ~2pp gap.
2. Floor ratchet to `floor(measured - 2pp)`.

OR (B) if dead code:

1. File spillover slice 312 to retire `internal/auth/bearer`.
2. Close this slice without merging.

## Acceptance criteria

- [ ] **AC-1.** Either merged coverage lifts to ≥ 70% AND floor
      ratchets to `floor(measured - 2pp)`, OR the package is
      confirmed dead-code and a retire-slice is filed.
- [ ] **AC-2.** Tests (if added) exercise real branches with real
      assertions.
- [ ] **AC-3.** No vendor-prefixed tokens in test fixtures.

## Constitutional invariants honored

- Testing discipline (CLAUDE.md).
- Slice 069 ratchet methodology.
- If retiring: slice 197's bearer-middleware retirement pattern.

## Dependencies

- None.

## Anti-criteria (P0 — block merge)

- **P0-311-1.** Does NOT raise the floor without tests.
- **P0-311-2.** Does NOT lower any existing floor.
- **P0-311-3.** Does NOT modify `_STATUS.md` from inside the slice.
- **P0-311-4.** Does NOT delete the package via this slice — if dead,
  file the retirement as a separate spillover (clean
  separation of concerns).

## Skill mix

- Existing `internal/auth/bearer/*_test.go` reading
- Cross-reference grep: `grep -rn "auth/bearer" internal/ cmd/` to
  determine whether it's still imported

## Notes for the implementing agent

First action: `grep -rn '"github.com/mgoodric/security-atlas/internal/auth/bearer"' .`
to see if the package is still referenced. If only the package's own
tests import it, it's dead code → file retirement spillover.

If it IS still in use, the lift is probably 2-3 new test cases for the
uncovered error-wrap branches. Use the slice 069 ratchet contract: lift
floor to `floor(measured - 2pp)` only after the tests land.
