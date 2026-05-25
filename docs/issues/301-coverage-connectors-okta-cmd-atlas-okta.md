# 301 — Coverage lift — `connectors/okta/cmd/atlas-okta` to 70%+

**Cluster:** Quality
**Estimate:** 1d (small package) to 3d (large package, see notes)
**Type:** AFK
**Status:** `not-ready`

## Narrative

Surfaced during slice 279's coverage audit, captured per the
continuous-batch policy. The audit at
`docs/coverage-audit-2026-05.md` measured `connectors/okta/cmd/atlas-okta` at **20.7% merged
coverage** (unit-only: 20.7%), below the 70% aspirational target the
slice established. Slice 279 lifted five highest-leverage packages and
filed the remaining `unit-add` long tail as per-package spillovers.

**Disposition:** `exempt-leaning`

**Notes:** tier 'CLI cmd'

## What ships in this slice

1. **New unit tests** under `connectors/okta/cmd/atlas-okta/*_test.go` covering the
   uncovered branches identified by the slice 279 audit.
2. **Floor ratchet** in `cmd/scripts/coverage-thresholds.json` from
   the current `18` to `floor(measured - 2pp)` where
   `measured` is the post-test merged %.

The two changes ship in the SAME PR per slice 069's ratchet contract
(no floor lift without tests; no tests without a floor lift).

## Acceptance criteria

- [ ] **AC-1.** New unit tests for `connectors/okta/cmd/atlas-okta` move its merged coverage
      to ≥ 70%.
- [ ] **AC-2.** Each test exercises real branches with real assertions
      (no vacuous `expect(true).toBe(true)` patterns).
- [ ] **AC-3.** Each new test file's first comment block names the
      package's load-bearing functions + the branches the file is
      designed to cover.
- [ ] **AC-4.** `coverage-thresholds.json` ratchets the `connectors/okta/cmd/atlas-okta` floor
      to merged-measured minus 2pp.

## Constitutional invariants honored

- **Testing discipline (CLAUDE.md "Testing discipline" section).**
  Ratchet contract: no floor raised without test added; no test added
  without floor raised.
- **Slice 069 methodology.** Floors ratchet at
  `max(0, floor(measured - 2pp))`. This slice does NOT lift floors
  above measured.

## Dependencies

- **#279** (coverage audit + targeted lift) — must merge first; this
  slice flips from `not-ready` to `ready` after #279 lands.

## Anti-criteria (P0 — block merge)

- **P0-301-1.** Does NOT raise the `connectors/okta/cmd/atlas-okta` floor without writing
  the unit tests that hit the new bar.
- **P0-301-2.** Does NOT lower any existing floor — every change to
  `thresholds` is monotonically ↑.
- **P0-301-3.** Does NOT modify `_STATUS.md` from inside this
  slice's own commits — orchestrator's surface.

## Notes for the implementing agent

The slice 279 audit at `docs/coverage-audit-2026-05.md` documents the
package's pre-lift surface. Read the relevant row for the disposition
notes; then run:

```bash
go test -coverpkg=./... -coverprofile=unit.cov ./...
go test -tags=integration -p 1 -coverpkg=./... -coverprofile=integration.cov <CI test list>
gocovmerge unit.cov integration.cov > merged.cov
go tool cover -func=merged.cov | grep 'connectors/okta/cmd/atlas-okta'
```

to see the per-function gap. Pick the largest pure-Go functions first;
DB-touching code likely needs an integration test (or already has one
that's not in the CI integration list, in which case adding the package
to the CI list is the right move — see how slice 279 did exactly this
for `internal/frameworkscope` and `internal/risk`).
