# 319 — Coverage lift — `internal/questionnaire` engine to 70%+

**Cluster:** Quality
**Estimate:** 2d (324 statements; questionnaire engine logic)
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 312's round-3 coverage audit, captured per the continuous-batch policy. The audit at `docs/coverage-audit-2026-05-round-3.md` measured `internal/questionnaire` at **26.5% merged coverage** (unit-only: 26.2%), below the 70% aspirational target.

`internal/questionnaire` is the questionnaire engine — vendor questionnaire (CAIQ / SIG / HECVAT) ingestion, similarity matching, AI-assisted draft generation (with mandatory human approval per canvas §4.6.5). Distinct from `internal/api/questionnaires` (HTTP handler, slice 316's surface).

**Disposition:** `unit-add`

**Notes:** Standalone slice because the engine logic (similarity scoring, citation enforcement, response template rendering) is distinct from the HTTP handler. Bundling would mix the layers.

## What ships in this slice

1. **New unit tests** under `internal/questionnaire/*_test.go` covering the engine surface.
2. **Possibly enroll in CI's `tests-integration` job** if the package has DB-touching paths (vendor questionnaire response store).
3. **Floor lift in `cmd/scripts/coverage-thresholds.json`** — add the new entry at `floor(merged_measured - 2pp)`.

## Acceptance criteria

- [ ] **AC-1.** `internal/questionnaire` reaches ≥ 70% merged coverage.
- [ ] **AC-2.** Each test exercises real branches with real assertions.
- [ ] **AC-3.** Each new test file's first comment block names load-bearing functions + branches covered.
- [ ] **AC-4.** `coverage-thresholds.json` adds the `internal/questionnaire` floor at `max(0, floor(measured - 2pp))`.

## Constitutional invariants honored

- **Testing discipline (CLAUDE.md).** Ratchet contract.
- **Slice 069 methodology.** Floor at `max(0, floor(measured - 2pp))`.
- **AI-assist boundary (canvas §4.6.5).** Questionnaire AI-assist must enforce mandatory citations — tests must assert that drafts WITHOUT citations are rejected.

## Dependencies

- **#312** (round-3 coverage audit + lift) — `ready`.

## Anti-criteria (P0 — block merge)

- **P0-319-1.** Does NOT raise the `internal/questionnaire` floor without writing the unit tests.
- **P0-319-2.** Does NOT lower any existing floor.
- **P0-319-3.** Does NOT modify `_STATUS.md` from inside this slice's own commits.
- **P0-319-4.** Does NOT bundle `internal/api/questionnaires` (slice 316) — that's the HTTP-handler-enrollment surface.

## Notes for the implementing agent

Read the slice that introduced the questionnaire engine (likely slice ~135 from the canvas §4.6 questionnaire roadmap) for the load-bearing functions. Pair tests with the existing patterns in `internal/risk` and `internal/decision` (both have rich unit-test surfaces for engine-style code).
