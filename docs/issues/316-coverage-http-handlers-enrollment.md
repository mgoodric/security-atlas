# 316 — Coverage lift — HTTP handler integration-enrollment (calendar + search + questionnaires)

**Cluster:** Quality
**Estimate:** 2-3d (3 packages share slice-290 enrollment pattern)
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 312's round-3 coverage audit, captured per the continuous-batch policy. The audit at `docs/coverage-audit-2026-05-round-3.md` identified 3 untracked HTTP handler packages below 70% merged coverage:

| Package                       | Unit-only % | Merged % | Statements |
| ----------------------------- | ----------- | -------- | ---------- |
| `internal/api/calendar`       | 38.1        | 40.4     | 223        |
| `internal/api/search`         | 31.8        | 32.2     | 214        |
| `internal/api/questionnaires` | 0.0         | 5.4      | 147        |

All 3 share the same shape: HTTP handler package, not currently enrolled in CI's `tests-integration` job list, likely already has an `integration_test.go` from its parent slice. Grouped because the enrollment pattern (slice 290 / 291 / 293 / 297 / 310) repeats verbatim.

**Disposition:** `unit-add` + `integration-enrollment`

**Notes:**

- `calendar` (slice ~125 — board / audit calendar)
- `search` (slice ~155 — universal search)
- `questionnaires` (slice ~135 — vendor questionnaire HTTP layer; companion to `internal/questionnaire` engine — slice 319 handles that separately)

## What ships in this slice

1. **Enroll the 3 packages in CI's `tests-integration` job** by adding `./internal/api/calendar/...`, `./internal/api/search/...`, `./internal/api/questionnaires/...`.
2. **New unit tests** for the pure-Go pre-DB branches (auth, URL params, JSON body, wire conversion, error responses).
3. **Floor lifts in `cmd/scripts/coverage-thresholds.json`** — add 3 new entries at `floor(merged_measured - 2pp)` each.

## Acceptance criteria

- [ ] **AC-1.** All 3 packages enrolled in CI's `tests-integration` job package list.
- [ ] **AC-2.** Each of the 3 packages reaches ≥ 70% merged coverage.
- [ ] **AC-3.** Each new test file's first comment block names load-bearing functions + branches covered.
- [ ] **AC-4.** `coverage-thresholds.json` adds 3 new floors at `max(0, floor(measured - 2pp))` each.

## Constitutional invariants honored

- **Testing discipline (CLAUDE.md).** Ratchet contract.
- **Slice 069 methodology.** Floor at `max(0, floor(measured - 2pp))`.

## Dependencies

- **#312** (round-3 coverage audit + lift) — `ready`.

## Anti-criteria (P0 — block merge)

- **P0-316-1.** Does NOT raise any floor without writing the unit tests + integration enrollment.
- **P0-316-2.** Does NOT lower any existing floor.
- **P0-316-3.** Does NOT modify `_STATUS.md` from inside this slice's own commits.
- **P0-316-4.** Does NOT bundle the `internal/questionnaire` engine work (slice 319) — that's a separate engine surface from the HTTP handler.

## Notes for the implementing agent

If any of the 3 packages does NOT have an `integration_test.go`, file a sub-spillover OR scope down. Don't write integration tests inline — pair-write them with the slice that owns the feature surface.

The slice 290 / 291 / 293 / 297 / 310 PRs are the reference pattern. Read one before starting.
