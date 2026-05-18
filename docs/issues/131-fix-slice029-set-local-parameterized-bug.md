# 131 — Fix slice 029 integration tests' `SET LOCAL $1` syntax error

**Cluster:** Backend (test infra)
**Estimate:** 0.25d
**Type:** AFK
**Status:** `not-ready`
**Parent:** spillover from 126 (discovered while running the slice-126 integration suite against the 9 wired packages)

## Narrative

Two integration tests in `internal/audit/notes/integration_slice029_test.go` fail at the parameter binding step:

- `TestSlice029_AppendOnlyAtDBLayer` — line 557
- `TestSlice029_DeleteRejectedAtDBLayer` — line 615 (approx)

Both call:

```go
conn.Exec(ctx, "SET LOCAL app.tenant_id = $1", tenant)
```

PostgreSQL's `SET` command does not accept bind parameters — the `$1` is a syntax error (`SQLSTATE 42601: syntax error at or near "$1"`). The tests have been broken since slice 029 landed (PR #71, commit `a335e40`). The failure was not previously observed because the broader integration test suite is not run as a default `make` target — it requires `DATABASE_URL_APP` to be set.

## Acceptance criteria

- [ ] AC-1: Replace the broken `SET LOCAL app.tenant_id = $1` parameterized call with the canonical `tenancy.ApplyTenant(ctx, tx)` pattern (which uses `SET LOCAL` with a literal-formatted UUID after `uuid.Parse` validation — same idiom used by 12+ other integration tests in this repo).
- [ ] AC-2: Both `TestSlice029_AppendOnlyAtDBLayer` and `TestSlice029_DeleteRejectedAtDBLayer` PASS in CI's `Go · integration (Postgres RLS)` job.
- [ ] AC-3: No production behavior change. The tests assert the same RLS append-only / delete-rejected properties they always did; only the harness's tenant-context-setting call changes.

## Dependencies

None (pure test fix).

## Anti-criteria (P0)

- **P0-A1**: Does NOT change the production `internal/audit/notes/notes.go` package — pure test-harness fix.
- **P0-A2**: Does NOT alter the RLS policies on `audit_notes` — the tests still assert the same property post-fix.

## Notes

Discovered while running the slice-126 integration suite via `go test -tags=integration -p 1 -count=1 ./internal/audit/...`. The slice-126 PR explicitly does NOT fix this finding (spillover Amendment 2: out-of-scope findings file as their own slice). The fix should land in a separate PR scoped to the test harness only.
