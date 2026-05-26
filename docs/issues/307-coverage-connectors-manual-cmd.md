# 307 — Coverage lift — `connectors/manual/cmd/atlas-manual` to 70%+

**Cluster:** Quality
**Estimate:** 0.5-1d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during the post-batch-122 audit. `connectors/manual/cmd/atlas-manual`
sits at floor `41` in `cmd/scripts/coverage-thresholds.json`. This is
the manual evidence connector — the CSV / S3 / SFTP / upload pathway
documented in canvas §4.5 as the "manual evidence is first-class"
invariant.

Same playbook as slices 299 / 302 / 303 / 300 / 301: cobra glue +
helpers + resolve / dial / authed context patterns. The manual
connector probably has more complex pull semantics than the other
connectors (file-source enumeration, MIME detection, etc.) so the
seam-refactor ceiling may be lower than 70%.

**Disposition:** `unit-add`

## What ships in this slice

1. **New `cmd_coverage_test.go`** under
   `connectors/manual/cmd/atlas-manual/` covering the cobra glue +
   helpers.
2. **Floor ratchet** in `cmd/scripts/coverage-thresholds.json` from
   41 to `floor(measured - 2pp)` (monotonically ↑).

## Acceptance criteria

- [ ] **AC-1.** Merged coverage moves meaningfully (target 70%+; partial
      accepted with spillover slice per slice 299 precedent).
- [ ] **AC-2.** Tests exercise real branches with real assertions.
- [ ] **AC-3.** File header names load-bearing functions + branches.
- [ ] **AC-4.** Floor ratchets to `floor(measured - 2pp)`.

## Constitutional invariants honored

- Testing discipline (CLAUDE.md): floor + tests in same PR.
- Slice 069 ratchet methodology.
- Canvas §4.5: manual evidence is first-class; tests must exercise
  the full ingest path (CSV / S3 / SFTP / upload), not just the
  happiest path.

## Dependencies

- None.

## Anti-criteria (P0 — block merge)

- **P0-307-1.** Does NOT raise the floor without the tests.
- **P0-307-2.** Does NOT lower any existing floor.
- **P0-307-3.** Does NOT modify `_STATUS.md` from inside the slice.
- **P0-307-4.** Does NOT use vendor-prefixed tokens; use neutral
  `test-*` strings.

## Skill mix

- Slice 299/302/303 exemplar reading
- File-source pattern testing (filesystem + S3-mock + SFTP-mock if
  applicable)

## Notes for the implementing agent

Manual connector likely has multiple subcommands (push from CSV /
upload / SFTP). Each subcommand's PreRunE + helper functions are
the prime coverage targets. The `osquery` cmd test from slice 303 is
the closest analogue (highest landing without seam refactor).
