# `internal/dbtest` — shared integration-test DB/tenant harness

`dbtest` is the canonical helper for security-atlas integration suites. New
integration tests **use `dbtest` instead of re-deriving** the pool / DSN /
tenant-seed / tenant-context boilerplate. It extracts the pattern the
canonical `internal/db/integration_test.go` `TestMain` established and that
~80 integration suites under `internal/` each used to copy-paste (the
rediscovery cost named in slice 353's Q-2). Slice 435 introduced it.

> **Build tag.** Everything here is `//go:build integration`. The helpers
> read `DATABASE_URL_APP` / `DATABASE_URL` and skip the test when unset —
> the standard integration-suite contract.

## The three primitives

| Helper                                     | Role                                                     | Use it for                                                                                                                                                                                                                        |
| ------------------------------------------ | -------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `dbtest.NewAppPool(t)`                     | `atlas_app` — **NOSUPERUSER NOBYPASSRLS** (RLS enforced) | Every RLS-bound read/write and every cross-tenant isolation assertion. **This is the default.**                                                                                                                                   |
| `dbtest.NewMigratePool(t)`                 | privileged **BYPASSRLS** (`DATABASE_URL`)                | Cleaning append-only tables the app role cannot `DELETE` from (`evidence_records`, `evidence_audit_log`, `control_drift_snapshots` — the slice-013 append-only RLS shape) and seeding fixtures across tenants.                    |
| `dbtest.SeedTenant(t, migrate, tables...)` | —                                                        | A fresh tenant id per call (no cross-test leak) + a `t.Cleanup` that `DELETE`s its rows from `tables` (FK-safe order: children before parents) through the migrate pool.                                                          |
| `dbtest.WithTenantCtx(t, tenant)`          | —                                                        | A `context.Context` tagged with the tenant via `tenancy.WithTenant` — the same path production uses. The `dbx` `Store` (or an explicit `tenancy.ApplyTenant` inside a tx) then sets the `app.current_tenant` GUC per transaction. |

Each primitive is `t`-scoped and registers its own `t.Cleanup` teardown
(pool close, tenant-row cleanup). No package-global mutable pool that bleeds
across tests.

## The load-bearing rule (do not break it)

**Two clearly-named constructors, RLS-enforcing default, privilege opt-in.**
The role boundary is the privilege boundary (CLAUDE.md invariant #6 — tenant
isolation is enforced at the DB layer via Row-Level Security; the integration
tier is what _proves_ it holds). The harness **never** silently hands back a
privileged pool where an app-role pool is expected. A test that asserts RLS
must run through `NewAppPool`; reach for `NewMigratePool` only for cleanup /
cross-tenant fixture seeding, and never use it for an RLS assertion — through
a BYPASSRLS pool, the assertion proves nothing.

`internal/dbtest/dbtest_test.go` carries the RLS-fidelity guards that lock
this in: a cross-tenant read is **still denied** through a `NewAppPool` pool
(write under tenant A, switch context to tenant B via `WithTenantCtx`, assert
zero rows), the app pool sees zero rows with no tenant GUC set, and the
migrate pool _does_ see the row — proving the two constructors return
different roles.

## Canonical usage

```go
//go:build integration

func TestSomething_RLSBound(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)            // BYPASSRLS — cleanup only
	app := dbtest.NewAppPool(t)                    // atlas_app — RLS enforced

	tenant := dbtest.SeedTenant(t, migrate,        // fresh tenant + cleanup
		"risk_control_links",                      // children first
		"risks",
		"controls",                                // parents last
	)
	store := risk.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)         // tenant-tagged context

	// ... exercise store through ctx; RLS is enforced exactly as in prod ...
}
```

## Migrated suites (slice 435)

The representative subset migrated in slice 435 — spanning the three real
call shapes — is `internal/scope`, `internal/risk`, and
`internal/freshnessdrift`. The remaining ~80 suites that still re-derive the
boilerplate are a documented follow-on drain (mirror of the slice-390 /
402-408 integration-enrolment drain): see the slice-435 decisions log
(`docs/audit-log/435-dbtest-harness-decisions.md`) for the spillover slice.

## Enrolment

`internal/dbtest` ships `integration`-tagged tests, so it is enrolled in the
integration job's package list (`scripts/integration-shards.txt`, Leg A,
alongside `internal/db`). A package that ships an `integration_test.go` but is
not enrolled silently runs no integration tests in CI — `just
audit-integration-enrolment` (slice 345) fails on that gap.
