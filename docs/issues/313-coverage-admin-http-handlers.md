# 313 — Coverage lift — admin HTTP handlers (5 packages, integration-enrollment)

**Cluster:** Quality
**Estimate:** 2-3d (5 packages share the slice-290 integration-enrollment pattern; per-package wiring + unit tests for pure-Go helpers)
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 312's round-3 coverage audit, captured per the continuous-batch policy. The audit at `docs/coverage-audit-2026-05-round-3.md` identified 5 untracked admin HTTP handler packages all below 70% merged coverage:

| Package                          | Unit-only % | Merged % | Statements |
| -------------------------------- | ----------- | -------- | ---------- |
| `internal/api/adminauditperiods` | 0.0         | 0.9      | 111        |
| `internal/api/adminsuperadmins`  | 0.0         | 0.6      | 165        |
| `internal/api/admintenants`      | 0.0         | 0.5      | 188        |
| `internal/api/adminvendors`      | 6.9         | 7.7      | 130        |
| `internal/api/tenants`           | 0.0         | 0.9      | 110        |

All 5 share the same shape: admin-scoped HTTP handler, no integration test enrolled in CI's `tests-integration` job, no unit tests for the pure-Go helpers (URL params, JSON validation, wire conversion). They are grouped into a single spillover slice because each is < 200 statements and the integration-enrollment pattern (slice 290 / 291 / 293 / 297 / 310) repeats verbatim across all 5.

**Disposition:** `unit-add` + `integration-enrollment`

**Notes:** Each package likely already has an `integration_test.go` from its parent slice (super-admin management = slice 142; admin tenants management = slice ~141; admin vendor management = slice ~140); enrolling them in CI's `tests-integration` job list is the load-bearing move. Pair with new unit tests for the pre-DB branches (auth + admin gate + URL param parsing + JSON body validation).

## What ships in this slice

1. **Enroll the 5 packages in CI's `tests-integration` job** by adding `./internal/api/adminauditperiods/...`, `./internal/api/adminsuperadmins/...`, etc. to the `go test -tags=integration` package list in `.github/workflows/ci.yml` (the same step that slices 290 / 291 / 293 / 297 / 310 extended).
2. **New unit tests** under `internal/api/admin*/helpers_test.go` (or similar) covering the pure-Go pre-DB branches: auth middleware, admin role gate, URL param parsers, JSON body validators, wire-conversion helpers, response writers (the 290-pattern split between integration-tested DB-touching paths and unit-tested pure-Go helpers).
3. **Floor lift in `cmd/scripts/coverage-thresholds.json`** — add 5 new entries at `floor(merged_measured - 2pp)` each. (Currently all 5 are untracked.)

The 3 changes ship in the SAME PR per slice 069's ratchet contract.

## Acceptance criteria

- [ ] **AC-1.** All 5 packages enrolled in CI's `tests-integration` job package list.
- [ ] **AC-2.** Each of the 5 packages reaches ≥ 70% merged coverage after enrollment + new unit tests.
- [ ] **AC-3.** Each new test file's first comment block names the load-bearing functions + the branches the file is designed to cover.
- [ ] **AC-4.** `coverage-thresholds.json` adds the 5 new floors at `max(0, floor(measured - 2pp))`.

## Constitutional invariants honored

- **Testing discipline (CLAUDE.md).** Ratchet contract: no floor raised without test added; no test added without floor raised.
- **Slice 069 methodology.** Floors at `max(0, floor(measured - 2pp))`. Monotonic ↑.

## Dependencies

- **#312** (round-3 coverage audit + lift) — `ready`. This slice flips from `not-ready` to `ready` after #312 merges (where the audit doc lands).

## Anti-criteria (P0 — block merge)

- **P0-313-1.** Does NOT raise any floor without writing the unit tests + integration enrollment that hit the new bar.
- **P0-313-2.** Does NOT lower any existing floor — every change to `thresholds` is monotonically ↑.
- **P0-313-3.** Does NOT modify `_STATUS.md` from inside this slice's own commits — orchestrator's surface.

## Notes for the implementing agent

Slice 312's audit doc documents each package's pre-lift surface. Read the relevant rows for disposition notes; then run:

```bash
go test -coverpkg=./... -coverprofile=unit.cov ./...
go test -tags=integration -p 1 -coverpkg=./... -coverprofile=integration.cov \
  <CI test list + the 5 new package globs>
gocovmerge unit.cov integration.cov > merged.cov
go tool cover -func=merged.cov | grep -E 'internal/api/(admin|tenants)/'
```

to see the per-function gap. The likely shape: each package has an `integration_test.go` already (from its parent slice — super-admin management slice 142, etc.) that's not currently in CI's list. Enrolling it is the load-bearing move. The unit tests cover pre-DB branches (auth + admin + URL parsing + JSON body) per the slice 290 / 291 / 293 / 297 / 310 pattern.

If any of the 5 packages does NOT yet have an `integration_test.go`, file a sub-spillover OR scope-shrink this slice to the 3-4 that do. Don't write integration tests inline — those are bigger than enrollment.
