# 315 — Coverage lift — auth-substrate-v2 small packages (4 packages)

**Cluster:** Quality
**Estimate:** 2-3d (4 small packages — each < 100 statements)
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 312's round-3 coverage audit, captured per the continuous-batch policy. The audit at `docs/coverage-audit-2026-05-round-3.md` identified 4 small auth-substrate-v2 packages all below 70% merged coverage:

| Package                     | Unit-only % | Merged % | Statements | Notes                                             |
| --------------------------- | ----------- | -------- | ---------- | ------------------------------------------------- |
| `internal/auth/oauthclient` | 5.7         | 5.7      | 53         | OAuth client registry (RFC 7591 / 7592 / dyn-reg) |
| `internal/auth/oauthcode`   | 0.0         | 0.0      | 89         | PKCE auth code store (RFC 7636)                   |
| `internal/auth/revocation`  | 18.2        | 18.2     | 44         | Revocation list (RFC 7009)                        |
| `internal/auth/userprefs`   | 27.8        | 29.6     | 54         | User preferences (theme, notifications, etc.)     |

All 4 are slice 187 / 188 / 192 OAuth AS substrate helpers; each is small (≤ 100 statements). Grouped into a single spillover because: (a) each is too small to warrant its own slice, (b) all 4 are sibling packages under `internal/auth/`, (c) the test pattern repeats (store CRUD + RLS isolation + error branches).

**Disposition:** `unit-add` + (likely) `integration-enrollment`

**Notes:** Each of these packages likely already has a basic integration test from its parent slice (slice 187 substrate, slice 192 OAuth AS endpoint completion, slice ~150 user prefs). The bulk of the lift is enrolling them in CI's `tests-integration` job and writing unit tests for any pure-Go helpers (cuid generation, predicate evaluation, etc.).

## What ships in this slice

1. **Enroll the 4 packages in CI's `tests-integration` job** (each as `./internal/auth/<pkg>/...`).
2. **New unit tests** under each `internal/auth/<pkg>/*_test.go` covering pure-Go helpers (the slice 290 split — integration covers DB-touching paths; unit covers pre-DB helpers).
3. **Floor lifts in `cmd/scripts/coverage-thresholds.json`** — add 4 new entries at `floor(merged_measured - 2pp)` each.

## Acceptance criteria

- [ ] **AC-1.** All 4 packages enrolled in CI's `tests-integration` job package list.
- [ ] **AC-2.** Each of the 4 packages reaches ≥ 70% merged coverage.
- [ ] **AC-3.** Each new test file's first comment block names the load-bearing functions + branches covered.
- [ ] **AC-4.** `coverage-thresholds.json` adds 4 new floors at `max(0, floor(measured - 2pp))` each.

## Constitutional invariants honored

- **Testing discipline (CLAUDE.md).** Ratchet contract: floor + tests in same PR.
- **Slice 069 methodology.** Floors at `max(0, floor(measured - 2pp))`. Monotonic ↑.
- **AI-assist boundary.** Auth-substrate is security infrastructure — no LLM-generated test bodies for revocation / code-store / client-registry branches.

## Dependencies

- **#312** (round-3 coverage audit + lift) — `ready`.
- **#187** (OAuth AS substrate foundation) — `merged`.
- **#192** (OAuth AS endpoint family) — should be `merged` before lift work begins.

## Anti-criteria (P0 — block merge)

- **P0-315-1.** Does NOT raise any floor without writing the unit tests + integration enrollment that hit the new bar.
- **P0-315-2.** Does NOT lower any existing floor.
- **P0-315-3.** Does NOT modify `_STATUS.md` from inside this slice's own commits.
- **P0-315-4.** Does NOT bundle this work with slice 314 (`internal/api/oauth`) — that's a separate 921-statement standalone slice.

## Notes for the implementing agent

The 4 packages cluster naturally:

- `oauthcode` + `oauthclient` — both RFC 7591 / 7636 / 6749 surface; pair their tests
- `revocation` — RFC 7009; standalone
- `userprefs` — non-OAuth; standalone

If any one of the 4 turns out to need a substantial seam refactor (unlikely given the small statement counts), file a sub-spillover and scope down to the 3 that don't. Don't grow this slice beyond the 70%-each target.
