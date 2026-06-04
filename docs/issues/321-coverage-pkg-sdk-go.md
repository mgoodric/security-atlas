# 321 — Coverage lift — `pkg/sdk-go` to 70%+

**Cluster:** Quality
**Estimate:** 0.5d (37 statements; 2.4pp gap; trivial)
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 312's round-3 coverage audit, captured per the continuous-batch policy. The audit at `docs/coverage-audit-2026-05-round-3.md` measured `pkg/sdk-go` at **67.6% merged coverage** (unit-only: 67.6%), 2.4pp below the 70% aspirational target.

`pkg/sdk-go` is the public Go push-SDK (slice 003). 37 statements — very small surface. The 2.4pp gap is 1 statement = roughly one uncovered branch in either `WithTLSConfig`, `NewClient`, `Close`, or `isLoopback` (per the per-function output from the slice 308 PR's CI merged-coverage artifact).

**Disposition:** `unit-add`

**Notes:** Quick win. Two or three additional unit tests (likely for `WithTLSConfig` and the `isLoopback` corner) should clear 70%.

## What ships in this slice

1. **New unit tests** under `pkg/sdk-go/*_test.go` covering the uncovered branches (likely TLS-config + loopback-detection corners).
2. **Floor lift in `cmd/scripts/coverage-thresholds.json`** — currently no entry (untracked). Add at `floor(measured - 2pp)` post-lift.

## Acceptance criteria

- [ ] **AC-1.** `pkg/sdk-go` reaches ≥ 70% merged coverage.
- [ ] **AC-2.** Each test exercises real branches with real assertions.
- [ ] **AC-3.** Each new test file's first comment block names load-bearing functions + branches covered.
- [ ] **AC-4.** `coverage-thresholds.json` adds the `pkg/sdk-go` floor at `max(0, floor(measured - 2pp))`.

## Constitutional invariants honored

- **Testing discipline (CLAUDE.md).** Ratchet contract.
- **Slice 069 methodology.** Floor at `max(0, floor(measured - 2pp))`.

## Dependencies

- **#312** (round-3 coverage audit + lift) — `ready`.
- Slice 003 (Evidence SDK) — `merged`.

## Anti-criteria (P0 — block merge)

- **P0-321-1.** Does NOT raise the `pkg/sdk-go` floor without writing the unit tests.
- **P0-321-2.** Does NOT lower any existing floor.
- **P0-321-3.** Does NOT modify `_STATUS.md` from inside this slice's own commits.

## Notes for the implementing agent

Trivial slice — should close in < 1 hour of test-writing. Run:

```bash
go test -coverpkg=./... -coverprofile=cov.out ./pkg/sdk-go/...
go tool cover -func=cov.out
```

to see the per-function gap. Pick the 1-2 uncovered functions; write a single test per uncovered branch. Done.

`pkg/sdk-go/oauth` (slice 188, already added to thresholds in slice 312 at floor 84) is the sibling package — separate; do not bundle.
