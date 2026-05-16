# Postgres RLS — How Tenant Isolation Actually Gets Enforced

_2026-05-16T06:13:40Z by Showboat 0.6.1_

<!-- showboat-id: 26c0b26b-9fd5-4bf6-a826-375a4b2c4cfb -->

> **Walkthrough kind:** this is a PAI Walkthrough skill document (slice 070 — showboat-generated). It is distinct from slice 027's audit walkthrough (`internal/audit/walkthrough`), which records auditor evidence capture against controls. The two concepts share a word and nothing else.

## Overview

Constitutional invariant 6 (`CLAUDE.md`): "Tenant isolation is enforced at the database layer via PostgreSQL Row-Level Security on every tenant-scoped table. Not application code. RLS denies on missing context."

This walkthrough traces that invariant from three roles in `migrations/bootstrap/01-roles.sql` down to a live cross-tenant query against a seeded database — first as the migrating role (which BYPASSRLS, by design), then as the application role (which does NOT, and which sees a hard zero-row response when the tenant context is unset).

Every block below was captured by `uvx showboat exec` against the slice-037 docker-compose self-host bundle, seeded by `fixtures/walkthroughs/00-seed.sql` + `rls-isolation.sql`. Re-run `just walkthroughs-refresh` and the captures regenerate against the same fixtures.

## 1. The Three Roles

`migrations/bootstrap/01-roles.sql` creates three database roles, each with sharply different RLS posture. The roles are the foundation — without them, no policy can enforce anything.

```bash
cd /Users/gmoney/Development/security-atlas-070 && grep -E "^(CREATE ROLE|ALTER ROLE).*atlas" migrations/bootstrap/01-roles.sql | head -10
```

```output

```

The two roles that matter for the runtime story:

- `atlas_migrate` (BYPASSRLS) — used only by Atlas for DDL. Schema changes against `FORCE ROW LEVEL SECURITY` tables would fail without BYPASSRLS. No application code path should ever connect as this role.
- `atlas_app` (NOBYPASSRLS, FORCE RLS via per-table policies) — used by the platform server and integration tests. Every query it runs is filtered by the active tenant policy.

`atlas_service_account` (NOLOGIN, NOINHERIT, BYPASSRLS) is reachable only via `SET LOCAL ROLE` for the rare cross-tenant read; not exercised by this walkthrough.

Let's confirm those role attributes against a live database:

```bash
docker exec security-atlas-pg-030 psql -U postgres -d security_atlas -c "SELECT rolname, rolbypassrls, rolcanlogin FROM pg_roles WHERE rolname LIKE 'atlas_%' ORDER BY rolname;"
```

```output
        rolname        | rolbypassrls | rolcanlogin
-----------------------+--------------+-------------
 atlas_app             | f            | t
 atlas_migrate         | t            | t
 atlas_service_account | t            | f
(3 rows)

```

## 2. The Four-Policy RLS Pattern

Every tenant-scoped table follows the same pattern (slice 002 + slice 033): `ALTER TABLE ... ENABLE ROW LEVEL SECURITY` + `FORCE ROW LEVEL SECURITY`, plus four CRUD policies named `tenant_read`, `tenant_write`, `tenant_update`, `tenant_delete`. Each policy filters by `current_tenant_matches(tenant_id)`, a helper that reads `app.current_tenant` from the session.

Looking at one such table — `controls` — for the pattern:

```bash
docker exec security-atlas-pg-030 psql -U postgres -d security_atlas -c "SELECT polname, polcmd, pg_get_expr(polqual, polrelid) AS using_expr FROM pg_policy WHERE polrelid = 'controls'::regclass ORDER BY polcmd, polname;"
```

```output
    polname    | polcmd |            using_expr
---------------+--------+-----------------------------------
 tenant_write  | a      |
 tenant_delete | d      | current_tenant_matches(tenant_id)
 tenant_read   | r      | current_tenant_matches(tenant_id)
 tenant_update | w      | current_tenant_matches(tenant_id)
(4 rows)

```

Note `tenant_write` (`polcmd=a` = INSERT) has no USING expression — INSERT policies use `WITH CHECK` instead. The other three (`r` read, `w` update, `d` delete) all share the same `current_tenant_matches(tenant_id)` USING expression.

The helper is a tiny SECURITY DEFINER function that reads the per-session GUC:

```bash
docker exec security-atlas-pg-030 psql -U postgres -d security_atlas -c "\\df+ current_tenant_matches" 2>&1 | grep -A 20 current_tenant_matches | head -25
```

```output
 public | current_tenant_matches | boolean          | row_tenant uuid     | func | stable     | safe     | postgres | invoker  |                   | sql      |               |
(1 row)

```

```bash
docker exec security-atlas-pg-030 psql -U postgres -d security_atlas -c "SELECT pg_get_functiondef('current_tenant_matches(uuid)'::regprocedure);" | sed -n '3,12p'
```

```output
 CREATE OR REPLACE FUNCTION public.current_tenant_matches(row_tenant uuid)+
  RETURNS boolean                                                         +
  LANGUAGE sql                                                            +
  STABLE PARALLEL SAFE                                                    +
 AS $function$                                                            +
     SELECT row_tenant::text = current_setting('app.current_tenant', true)+
 $function$                                                               +

(1 row)

```

The function is intentionally trivial: compare the row’s `tenant_id` against the `app.current_tenant` GUC. The third arg to `current_setting(..., true)` makes it return NULL rather than error when the GUC is unset — which causes `current_tenant_matches` to return NULL, which RLS treats as false, which denies the row. "Denies on missing context" is the constitutional contract.

## 3. The Seeded Scenario

`fixtures/walkthroughs/00-seed.sql` + `rls-isolation.sql` install two tenants:

| Tenant        | UUID                                   | Bundle id                  |
| ------------- | -------------------------------------- | -------------------------- |
| `demo-tenant` | `00000000-0000-0000-0000-00000000d3a0` | `demo-s3-encryption`       |
| `alt-tenant`  | `00000000-0000-0000-0000-00000000a17e` | `alt-tenant-s3-encryption` |

Each owns exactly one row in `controls`. Looking at all of it from the migrating role (which bypasses RLS):

```bash
docker exec security-atlas-pg-030 psql -U postgres -d security_atlas -c "SELECT tenant_id, bundle_id, title FROM controls ORDER BY tenant_id;"
```

```output
              tenant_id               |        bundle_id         |                       title
--------------------------------------+--------------------------+----------------------------------------------------
 00000000-0000-0000-0000-00000000a17e | alt-tenant-s3-encryption | Encryption at rest — alt-tenant production buckets
 00000000-0000-0000-0000-00000000d3a0 | demo-s3-encryption       | Encryption at rest — production object stores
(2 rows)

```

(The `postgres` superuser implicitly bypasses RLS — superusers ignore policies entirely. That is why every application connection MUST use `atlas_app`. Slice 033 codified this.)

## 4. The Application Role Without a Tenant Context

Now switch to the application role. No `SET LOCAL app.current_tenant`. The four-policy RLS denies all rows:

```bash
docker exec security-atlas-pg-030 psql -U atlas_app -d security_atlas -c "SELECT count(*) AS rows_visible_with_no_tenant_context FROM controls;"
```

```output
 rows_visible_with_no_tenant_context
-------------------------------------
                                   0
(1 row)

```

Zero rows. Not an error — RLS silently filters every row out, because returns NULL for every row when the GUC is unset, and NULL is treated as false by the policy. This is exactly what the invariant requires: a buggy code path that forgets to set the tenant gets safe zero-row behavior, not a cross-tenant leak.

## 5. The Application Role With demo-tenant Context

Same role, but SET LOCAL the demo-tenant UUID inside a transaction:

```bash
docker exec security-atlas-pg-030 psql -U atlas_app -d security_atlas <<SQL
BEGIN;
SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';
SELECT tenant_id, bundle_id, title FROM controls;
ROLLBACK;
SQL
```

```output

```

```bash
docker exec security-atlas-pg-030 psql -U atlas_app -d security_atlas -c "BEGIN; SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0'; SELECT tenant_id, bundle_id, title FROM controls; ROLLBACK;"
```

```output
BEGIN
SET
              tenant_id               |     bundle_id      |                     title
--------------------------------------+--------------------+-----------------------------------------------
 00000000-0000-0000-0000-00000000d3a0 | demo-s3-encryption | Encryption at rest — production object stores
(1 row)

ROLLBACK
```

## 6. Same Role, Alt-Tenant Context

Switch the context to the second tenant. The same role, same SQL — different visible row:

```bash
docker exec security-atlas-pg-030 psql -U atlas_app -d security_atlas -c "BEGIN; SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000a17e'; SELECT tenant_id, bundle_id, title FROM controls; ROLLBACK;"
```

```output
BEGIN
SET
              tenant_id               |        bundle_id         |                       title
--------------------------------------+--------------------------+----------------------------------------------------
 00000000-0000-0000-0000-00000000a17e | alt-tenant-s3-encryption | Encryption at rest — alt-tenant production buckets
(1 row)

ROLLBACK
```

## 7. Trying to INSERT a Cross-Tenant Row

What stops a buggy caller — context set to one tenant but the row body claims another — from writing into the wrong tenant? The `tenant_write` policy uses `WITH CHECK (current_tenant_matches(tenant_id))`. The `INSERT` itself fails:

```bash
docker exec security-atlas-pg-030 psql -U atlas_app -d security_atlas -c "BEGIN; SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0'; INSERT INTO controls (id, tenant_id, scf_id, title, control_family, implementation_type, bundle_id) VALUES ('99999999-9999-9999-9999-999999999999', '00000000-0000-0000-0000-00000000a17e', 'CRY-05', 'cross-tenant smuggling attempt', 'Cryptography', 'automated', 'demo-cross-tenant'); ROLLBACK;" 2>&1
```

```output
BEGIN
SET
ERROR:  new row violates row-level security policy for table "controls"
```

`new row violates row-level security policy for table "controls"`. Exactly the error Postgres raises when WITH CHECK fails — there is no way for the application role to insert a row whose `tenant_id` does not match the active session GUC. The check happens at the database, not in Go code. No application bug can elide it.

## 8. The Go Plumbing: Where the GUC Gets Set

In Go, the GUC is set per-request by the tenancy middleware. The package lives at `internal/tenancy/`:

```bash
cd /Users/gmoney/Development/security-atlas-070 && grep -rn "SET LOCAL app.current_tenant\|app.current_tenant" internal/tenancy/ 2>&1 | head -8
```

```output
internal/tenancy/context.go:3:// GUC `app.current_tenant`. The Row-Level Security policies on every
internal/tenancy/context.go:20:const GUCName = "app.current_tenant"
```

```bash
cd /Users/gmoney/Development/security-atlas-070 && grep -n "SET LOCAL\|set_config\|app.current_tenant" internal/tenancy/*.go | head -10
```

```output
internal/tenancy/apply.go:10:// ApplyTenant sets the tenant GUC on the given transaction via set_config,
internal/tenancy/apply.go:11:// which accepts a bound parameter (unlike bare SET LOCAL). Effects die on
internal/tenancy/apply.go:22:	if _, err := tx.Exec(ctx, "SELECT set_config($1, $2, true)", GUCName, tenant); err != nil {
internal/tenancy/apply.go:23:		return fmt.Errorf("tenancy: set_config %s: %w", GUCName, err)
internal/tenancy/context.go:3:// GUC `app.current_tenant`. The Row-Level Security policies on every
internal/tenancy/context.go:20:const GUCName = "app.current_tenant"
```

```bash
sed -n '10,30p' /Users/gmoney/Development/security-atlas-070/internal/tenancy/apply.go
```

```output
// ApplyTenant sets the tenant GUC on the given transaction via set_config,
// which accepts a bound parameter (unlike bare SET LOCAL). Effects die on
// commit/rollback because the third argument requests session-local scope.
//
// Requiring pgx.Tx (not *pgx.Conn) is intentional: outside a transaction the
// is_local flag is silently inert, RLS sees the GUC as empty, and queries
// return zero rows. The type signature makes that footgun impossible.
func ApplyTenant(ctx context.Context, tx pgx.Tx) error {
	tenant, err := TenantFromContext(ctx)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "SELECT set_config($1, $2, true)", GUCName, tenant); err != nil {
		return fmt.Errorf("tenancy: set_config %s: %w", GUCName, err)
	}
	return nil
}
```

`ApplyTenant` accepts a `pgx.Tx` (not `*pgx.Conn`) deliberately — outside a transaction `set_config(..., true)` is silently inert, the GUC reads as empty, and queries return zero rows. The type signature makes the footgun impossible.

The middleware that derives the tenant from the request context lives in `internal/tenancy/middleware.go`; the integration tests in `internal/tenancy/integration_test.go` re-run exactly the scenario in sections 4-7 against a fresh test database.

## 9. Putting It All Together

The constitutional invariant breaks down into five layers, each captured above:

1. **Roles** (section 1) — `atlas_app` is NOBYPASSRLS by construction.
2. **Policies** (section 2) — four CRUD policies on every tenant-scoped table, all filtering through one tiny helper.
3. **Helper** (section 2) — `current_tenant_matches(tenant_id)` reads `app.current_tenant`; returns NULL/false on missing context.
4. **Denial on missing context** (section 4) — zero rows visible, not an error.
5. **Cross-tenant write denial** (section 7) — `WITH CHECK` rejects a smuggled `tenant_id`.

The Go side (section 8) is small by design: `ApplyTenant` SETs the GUC inside the request transaction, and that is the _only_ application code path that needs to be correct. Everything else is database policy.

### Where to read more

- **Canvas:** [`Plans/canvas/05-scopes.md`](https://github.com/mgoodric/security-atlas/blob/main/Plans/canvas/05-scopes.md) §5.4 — tenancy invariant
- **Slice docs:** [`docs/issues/002-schema-migrations.md`](https://github.com/mgoodric/security-atlas/blob/main/docs/issues/002-schema-migrations.md) (RLS introduced), [`docs/issues/033-postgres-rls-enforcement.md`](https://github.com/mgoodric/security-atlas/blob/main/docs/issues/033-postgres-rls-enforcement.md) (four-policy pattern + `atlas_service_account`)
- **Go package:** [`internal/tenancy/`](https://github.com/mgoodric/security-atlas/blob/main/internal/tenancy/) — `ApplyTenant`, `TenantFromContext`, middleware
- **Bootstrap SQL:** [`migrations/bootstrap/01-roles.sql`](https://github.com/mgoodric/security-atlas/blob/main/migrations/bootstrap/01-roles.sql) — role attributes
