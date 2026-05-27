# 318 — Coverage lift — audit ledger plumbing (3 packages)

**Cluster:** Quality
**Estimate:** 2-3d (3 packages share audit-log family)
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 312's round-3 coverage audit, captured per the continuous-batch policy. The audit at `docs/coverage-audit-2026-05-round-3.md` identified 3 untracked audit-log packages below 70% merged coverage:

| Package                     | Unit-only % | Merged % | Statements | Notes                                     |
| --------------------------- | ----------- | -------- | ---------- | ----------------------------------------- |
| `internal/audit`            | 0.0         | 0.4      | 231        | umbrella / shared types                   |
| `internal/audit/sink`       | 67.3        | 67.3     | 150        | append-only ledger writer (JUST below 70) |
| `internal/audit/unifiedlog` | 18.8        | 18.8     | 32         | unified-log entry shape (slice 124)       |

Grouped because all 3 are sibling packages under `internal/audit/` and all 3 are audit-log family. `internal/audit/sink` is just 2.7pp short of 70 — small unit additions clear it. The other 2 need substantive new tests.

**Disposition:** `unit-add` + (likely) `integration-enrollment`

**Notes:**

- `internal/audit` umbrella — likely shared types only; check if it has any business logic or if it's just type declarations. If types-only, file as exclude-tier instead.
- `internal/audit/sink` — slice ~030 audit-log writer; near-target, easy lift.
- `internal/audit/unifiedlog` — slice 124 unified-log shape; tiny, easy lift.

## What ships in this slice

1. **Inspect `internal/audit`** — if it's types-only, propose adding to `excludes`; if it has business logic, write unit tests for it.
2. **Enroll the 3 packages in CI's `tests-integration` job** (if not already and if they have integration tests).
3. **New unit tests** for pure-Go helpers in each.
4. **Floor lifts in `cmd/scripts/coverage-thresholds.json`** — add 2-3 new entries at `floor(merged_measured - 2pp)` each (depends on the `internal/audit` decision).

## Acceptance criteria

- [ ] **AC-1.** Each of the 3 packages either (a) reaches ≥ 70% merged coverage with floor at `floor(measured - 2pp)`, or (b) is documented as types-only and added to `excludes`.
- [ ] **AC-2.** Each new test file's first comment block names load-bearing functions + branches covered.
- [ ] **AC-3.** `coverage-thresholds.json` reflects the new floors OR excludes additions per AC-1.

## Constitutional invariants honored

- **Testing discipline (CLAUDE.md).** Ratchet contract.
- **Slice 069 methodology.** Floor at `max(0, floor(measured - 2pp))`.
- **Audit-log is constitutional invariant (canvas §8.4 + slice 124).** Tests must assert append-only semantics (no UPDATE / DELETE on the ledger) — vacuous tests rejected.

## Dependencies

- **#312** (round-3 coverage audit + lift) — `ready`.
- Slice 124 (unified audit log) — `merged`.

## Anti-criteria (P0 — block merge)

- **P0-318-1.** Does NOT raise any floor without writing the unit tests + (if applicable) integration enrollment.
- **P0-318-2.** Does NOT lower any existing floor.
- **P0-318-3.** Does NOT modify `_STATUS.md` from inside this slice's own commits.
- **P0-318-4.** Does NOT test in a way that violates the append-only invariant (e.g. UPDATE/DELETE on ledger rows in tests).

## Notes for the implementing agent

The `internal/audit/sink` package is only 2.7pp short of 70%. A small unit-test addition (5-8 tests) should suffice. The `internal/audit/unifiedlog` package is tiny (32 stmts) — a single test file should do it.

The `internal/audit` umbrella needs inspection first: if it's just type aliases / shared interfaces, the right disposition is `excludes`, not `thresholds`. Make the call in the slice's decisions log.
