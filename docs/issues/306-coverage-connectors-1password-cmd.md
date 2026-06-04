# 306 — Coverage lift — `connectors/1password/cmd/atlas-1password` to 70%+

**Cluster:** Quality
**Estimate:** 0.5-1d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during the post-batch-122 audit (round 2 of slice 279's
coverage long-tail). `connectors/1password/cmd/atlas-1password`
currently sits at floor `12` in
`cmd/scripts/coverage-thresholds.json` — the lowest non-exempt cmd
package on main after batch 122 closed.

The package is the cobra main glue for the 1Password connector binary
(register / run / scopes subcommands per the established
connector-cmd shape from slice 004). The pattern that worked for
slices 299 / 302 / 303 / 300 / 301 applies cleanly:

1. Read the existing test surface (likely just `buildRecord` / data
   conversion); cobra glue + helpers are uncovered.
2. Write `cmd_coverage_test.go` mirroring slice 303's structure
   (which scored the highest 70%+ landing without seam refactor):
   newRootCmd / newRegisterCmd / newRunCmd / newScopesCmd /
   resolveCommon / dialConnectorRegistry / authedContext / sdkOpts
   / mapResult.
3. Ratchet floor to `floor(measured - 2pp)` per slice 069.

If 70% requires a seam refactor (slice 305 pattern), STOP at whatever
is achievable without the refactor and file a spillover slice for the
seam (mirroring slice 305 for aws-connector).

**Disposition:** `unit-add`

**Notes:** baseline floor 12 suggests measured ~14% — close to
slice 301's github cmd starting point (15.1%) which landed at 58.8%
without seam refactor.

## What ships in this slice

1. **New `cmd_coverage_test.go`** under
   `connectors/1password/cmd/atlas-1password/` covering the cobra
   glue + helpers slice 299's template established.
2. **Floor ratchet** in `cmd/scripts/coverage-thresholds.json` from
   12 to `floor(measured - 2pp)` (monotonically ↑).

Both changes ship in the SAME PR per slice 069's ratchet contract.

## Acceptance criteria

- [ ] **AC-1.** New unit tests move
      `connectors/1password/cmd/atlas-1password` merged coverage
      meaningfully (target 70%+; partial-lift accepted with spillover
      slice if seam refactor needed, per slice 299 precedent).
- [ ] **AC-2.** Each test exercises real branches with real assertions.
- [ ] **AC-3.** Each new test file's first comment block names the
      load-bearing functions + branches.
- [ ] **AC-4.** `coverage-thresholds.json` ratchets the
      `connectors/1password/cmd/atlas-1password` floor to
      `floor(measured - 2pp)`.

## Constitutional invariants honored

- Testing discipline (CLAUDE.md): floor + tests in same PR.
- Slice 069 methodology: floor at `max(0, floor(measured - 2pp))`.
- Slice 305 seam pattern: only apply IF the cmd package needs it;
  otherwise file a spillover.

## Dependencies

- None — slice 279 audit + slice 305 seam pattern are both merged.

## Anti-criteria (P0 — block merge)

- **P0-306-1.** Does NOT raise the floor without the tests.
- **P0-306-2.** Does NOT lower any existing floor.
- **P0-306-3.** Does NOT modify `_STATUS.md` from inside the slice's
  own commits — orchestrator's surface.
- **P0-306-4.** Does NOT use vendor-prefixed tokens (`op_*` if
  1Password uses one; check the SDK; use neutral `test-*` strings).

## Skill mix

- Slice 299/302/303 exemplar reading
- cobra command testing patterns
- table-driven enum + branch coverage

## Notes for the implementing agent

The exemplar to mirror is
`connectors/osquery/cmd/atlas-osquery/cmd_coverage_test.go` (slice
303 — 28.2% → 82.4%; cleared 70% without seam refactor). If the
1password cmd shape diverges significantly (e.g. uses a different
auth flow), file a separate spillover with the specifics.
