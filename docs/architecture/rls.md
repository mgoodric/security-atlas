# Row-Level Security — multi-tenant isolation at the database layer

> Constitutional invariant 6 (CLAUDE.md): **Tenant isolation is enforced
> at the database layer via PostgreSQL Row-Level Security on every
> tenant-scoped table. Not application code. RLS denies on missing
> context.**

This document is the contributor reference for how RLS is layered into
security-atlas. Slice 033 landed the audit + middleware machinery; this
file explains what that machinery enforces and how to extend it without
breaking the invariant.

The canvas reference is `Plans/canvas/05-scopes.md` §5.4. This doc is
the implementation-facing companion.

---

## The two halves of the guarantee

Multi-tenancy in security-atlas is enforced by **two cooperating
pieces**:

1. **Schema** — every tenant-scoped table carries a `tenant_id UUID`
   column, `ENABLE ROW LEVEL SECURITY`, `FORCE ROW LEVEL SECURITY`, and
   at least one `CREATE POLICY` whose `USING` predicate compares
   `tenant_id` to the `app.current_tenant` GUC.
2. **Runtime** — every request that touches a tenant-scoped table
   begins by setting `app.current_tenant` from the authenticated
   credential's tenant id, BEFORE any SQL runs.

If half (1) is missing, RLS does not exist for that table — a wildcard
`SELECT` returns every tenant's rows. If half (2) is missing, RLS sees
an empty GUC and the `current_tenant_matches()` predicate returns
NULL/false — a `SELECT` returns zero rows. Both halves are necessary.

The slice-033 audit script (`just audit-rls`) checks half (1). The
slice-033 middleware (`internal/api/tenancymw`) sets up half (2).

---

## Half 1 — the schema invariant

### The helper function

`migrations/sql/20260511000000_init.sql` defines:

```sql
CREATE FUNCTION current_tenant_matches(row_tenant uuid)
RETURNS boolean
LANGUAGE sql STABLE PARALLEL SAFE
AS $$
    SELECT row_tenant::text = current_setting('app.current_tenant', true)
$$;
```

The `true` second argument to `current_setting` is the "missing OK"
flag: when the GUC is unset, `current_setting` returns NULL. Comparing
anything to NULL yields NULL, which in a `USING` clause is treated as
false. **There is no default-allow path.**

### The three valid policy patterns

Every tenant-scoped table in the schema uses one of these shapes. New
tables MUST use one of them; the audit script (`just audit-rls`) only
checks that a policy exists, not which shape — drift in policy shape
surfaces in human review of the migration.

#### Pattern A — single `tenant_isolation` policy

Used by the slice-002 init schema for tables whose access patterns
don't distinguish read from write:

```sql
ALTER TABLE controls ENABLE ROW LEVEL SECURITY;
ALTER TABLE controls FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON controls
    USING (current_tenant_matches(tenant_id));
```

A `USING`-only policy applies to SELECT/UPDATE/DELETE (the row-visibility
predicate). INSERT uses the `WITH CHECK` predicate; when `WITH CHECK`
is omitted, Postgres falls back to the `USING` predicate.

When to use: simplest tables, where every operation against a row in
the wrong tenant is equally wrong.

#### Pattern B — four-policy split

Used by slices 005, 006, 008, 009, 010, 011, 012 for tables that have
distinct read and write surfaces. The four policies are named after the
verbs they gate:

```sql
ALTER TABLE risks ENABLE ROW LEVEL SECURITY;
ALTER TABLE risks FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read   ON risks FOR SELECT USING       (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write  ON risks FOR INSERT WITH CHECK  (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON risks FOR UPDATE USING       (current_tenant_matches(tenant_id))
                                            WITH CHECK  (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON risks FOR DELETE USING       (current_tenant_matches(tenant_id));
```

When to use: when a future slice may need to differentiate per-verb
permissions (e.g. a read-only auditor role that uses
`tenant_read` while denying everything else by RLS). This is the
default for new tables.

#### Pattern C — append-only

Used by slice 004's `evidence_records` and `evidence_audit_log`, and
several other audit-log-shaped tables. Only `tenant_read` + a write
policy (`tenant_write` or `tenant_insert`) are declared; no UPDATE or
DELETE policy at all. Combined with `FORCE ROW LEVEL SECURITY`, the
table becomes append-only at the database layer:

```sql
ALTER TABLE evidence_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE evidence_records FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read   ON evidence_records FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_insert ON evidence_records FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
-- No UPDATE or DELETE policy. FORCE makes this an unconditional deny.
```

When to use: any ledger / audit-log / receipt table where modifying or
removing existing rows would break point-in-time replay or evidence
integrity. The schema enforces append-only; application code does not
have to be trusted to honor it.

### The catalog exception — `tenant_or_catalog`

`frameworks`, `framework_versions`, and `evidence_kind_schemas` carry a
**nullable** `tenant_id`. A NULL `tenant_id` means "global catalog,
visible to every tenant" (e.g. a bundled SCF anchor); a non-NULL value
means "tenant-private extension". The policy admits both:

```sql
CREATE POLICY tenant_or_catalog ON frameworks
    USING (tenant_id IS NULL OR current_tenant_matches(tenant_id));
```

The audit script considers this a valid pattern — the table has a
`tenant_id` column and at least one policy, which is all it requires.
The catalog semantics are enforced by the policy shape itself.

### `scf_anchors` — the no-tenant table

`scf_anchors` (slice 006) holds the global SCF concept catalog. It has
**no `tenant_id` column**. The audit script intentionally skips tables
without a `tenant_id` column — they're catalog data, not tenant data.

---

## Half 2 — the runtime invariant

### Where the GUC gets set

Every bearer-auth'd HTTP request flows through this middleware chain
(see `internal/api/httpserver.go`):

```
chi.Router
  └─ corsMiddleware                              (slice 005)
  └─ httpAuthMiddlewareWithExemptions(...)       (slice 034)
  └─ tenancymw.Middleware                        (slice 033)  ←
  └─ handler
```

`tenancymw.Middleware` reads `authctx.CredentialFromContext` and, if a
credential is present, calls `tenancy.WithTenant(ctx, cred.TenantID)`
which derives a child context tagged with the tenant id. The handler
then calls `tenancy.ApplyTenant(ctx, tx)` inside its transaction, which
issues `SELECT set_config('app.current_tenant', $1, true)` — the
`true` makes it transaction-local, so the GUC dies on COMMIT/ROLLBACK
and never leaks to the next pool checkout.

`tenancy.ApplyTenant` requires a `pgx.Tx`, not a `*pgx.Conn`. This
matters: outside a transaction, the `is_local` flag is silently inert
and RLS sees the GUC as empty. The type signature makes that footgun
impossible.

### The two no-tenant-on-context paths

`tenancymw.Middleware` deliberately **no-ops** when no credential is in
context. There are two legitimate paths where the credential is absent:

1. **Bearer-exempt prefixes** (currently `/auth/*`). The user has no
   bearer at the moment of sign-in; the auth middleware skips bearer
   parsing for these prefixes. The handlers themselves (`local-login`,
   `oidc-login`, `oidc-callback`, `logout`) take a request-supplied
   tenant id (body / query / cookie) and call `tenancy.WithTenant`
   directly. Because `context.WithValue` shadows, the handler's value
   overrides whatever the middleware did (which, for these paths, is
   nothing).

2. **The handler's own preflight** (currently `/v1/admin/credentials`
   Issue and List). These handlers take `tenant_id` from the request
   body / query — they were written before slice 033. The middleware
   still sets the credential's tenant onto the context, then the
   handler's own `tenancy.WithTenant(req.TenantID)` overrides it.
   **This is a known pre-existing authorization bug** (the caller's
   credential should be the only source of truth for the tenant; the
   request body should not be trusted). Filed as a follow-up issue;
   not fixed in slice 033 because RLS does not paper over it (the
   handler is internally consistent: writes tenant B's row under
   tenant B's GUC).

Every other handler in `internal/api/**` MUST inherit the tenant from
the middleware. The boilerplate that used to live in each handler:

```go
cred, ok := authctx.CredentialFromContext(r.Context())
if !ok || cred.TenantID == "" { ... }
ctx, err := tenancy.WithTenant(r.Context(), cred.TenantID)
if err != nil { ... }
```

is now simply:

```go
if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
    // 401-shaped path; the middleware did not run (exempt or unauth)
    return
}
ctx := r.Context()
```

### What happens on a malformed tenant id

`tenancymw.Middleware` calls `tenancy.WithTenant`, which validates the
tenant id is a UUID. A non-UUID id from the credstore means data-store
drift; the middleware fails the request with 500 (server-side bug)
rather than papering over it. This branch is covered by
`TestMiddleware_MalformedTenant_Fails500`.

---

## Bypass-RLS — the canonical exception

A small number of platform features need to read across tenants
(future: platform-wide audit-log scans, support tooling, fleet-level
metrics). These are the **only** legitimate use of `BYPASSRLS`. The
v1 platform has none of them, but the seam is in place so the first
such feature has a clean path.

### The role

`migrations/bootstrap/01-roles.sql` (slice 033 additions):

```sql
CREATE ROLE atlas_service_account NOLOGIN NOINHERIT BYPASSRLS;
GRANT atlas_service_account TO atlas_app;
```

- `BYPASSRLS` — what the role exists for. It can read across tenants.
- `NOLOGIN` — cannot be a session-establishing role from outside the
  server. The only path in is from within an existing `atlas_app`
  session.
- `NOINHERIT` — the privilege does NOT flow automatically to
  `atlas_app`. An `atlas_app` session does NOT bypass RLS until it
  explicitly switches role.

### The pattern

A caller that needs to break out of RLS (rare, audited, scoped to one
named operation) wraps a single transaction:

```go
tx, err := pool.Begin(ctx)
if err != nil { return err }
defer tx.Rollback(ctx)

if _, err := tx.Exec(ctx, "SET LOCAL ROLE atlas_service_account"); err != nil {
    return fmt.Errorf("switch to service account: %w", err)
}

// Cross-tenant read happens here. The transaction MUST be scoped to
// only the cross-tenant operation — do not mix it with same-tenant
// writes, and do not extend the lifetime beyond the operation.

return tx.Commit(ctx)
```

Properties:

- `SET LOCAL ROLE` only persists for the current transaction. COMMIT
  or ROLLBACK restores the connection to `atlas_app`.
- The role-switch is auditable: every BYPASSRLS read should be paired
  with an `evidence_audit_log` entry or an equivalent ledger row
  written **inside the same transaction**, so the audit trail and the
  cross-tenant action are atomic.
- Approved use-cases: platform-wide audit-log queries, future support
  tooling, fleet metrics. New use-cases require an ADR.

### What the pattern is NOT for

- **Same-tenant cross-row work** — that's regular RLS-bound access.
- **"Convenience" cross-tenant access** — there are no support
  short-cuts in v1; the same role-switch discipline applies to every
  caller.
- **Permanent ALTER atlas_app BYPASSRLS** — never. The role split is
  load-bearing.

---

## `just audit-rls` — the schema gate

`scripts/audit-rls.sh` is the CI gate that catches drift in the
schema-side half of the guarantee. It runs against `DATABASE_URL`
(the `atlas_migrate` connection string, which has BYPASSRLS for
pg_catalog visibility) and asserts:

For every public-schema table with a `tenant_id` column:

1. At least one RLS policy is attached, AND
2. `relforcerowsecurity = true`.

If either condition fails for any table, the script exits non-zero
with a tab-separated list of offenders to stdout. CI fails the build.

**What the script intentionally does NOT check:**

- Policy shape — single vs four-policy vs append-only is a human
  judgement call documented in the migration; the script trusts the
  migration author.
- Policy correctness — the audit doesn't try to confirm the
  `USING` predicate references `tenant_id` correctly. That's the job
  of the cross-tenant integration tests in
  `internal/db/rls_integration_test.go` — they SEED under tenant A,
  SWITCH to tenant B, and assert the row is invisible. Behaviour is
  the ground truth.

**Local invocation:**

```bash
DATABASE_URL='postgres://atlas_migrate@host:5432/security_atlas?sslmode=disable' \
  just audit-rls
```

**CI invocation:** wired into `.github/workflows/ci.yml` between the
"Apply forward migrations" step and the "Run integration tests" step.
A drift in the schema half of the invariant fails the build before any
behavioural tests even start.

---

## When you add a new tenant-scoped table

The checklist:

1. Add a `tenant_id UUID NOT NULL` column (or NULL if it's a
   catalog-style table — but then add the `tenant_or_catalog` policy
   pattern).
2. `ALTER TABLE ... ENABLE ROW LEVEL SECURITY`.
3. `ALTER TABLE ... FORCE ROW LEVEL SECURITY` (so the table owner is
   bound too; without it, the migration role would bypass).
4. Add one of the three patterns (single / four-policy / append-only).
5. GRANT SELECT/INSERT/UPDATE/DELETE on the table to `atlas_app` (the
   policies gate what `atlas_app` can see; the GRANT is what makes
   the verb reachable at all).
6. Add a cross-tenant negative to `internal/db/rls_integration_test.go`
   — extend `TestRLS_CrossTenant_SweepPerTable` with a new `cases`
   entry.
7. Run `just audit-rls` locally against an applied schema; the new
   table should pass.
8. Run `just test-integration`; the cross-tenant negative should pass.

Skip any step and the CI gate plus the cross-tenant negatives will
catch you. That's the point.

---

## Glossary

- **GUC** — Grand Unified Configuration. Postgres calls its
  per-session/per-transaction settings GUCs. `app.current_tenant` is
  the one we own.
- **FORCE ROW LEVEL SECURITY** — without this, the table owner
  (`atlas_migrate` in our setup) bypasses policies. We want the policy
  to apply uniformly, so we FORCE.
- **`is_local` flag** — third argument to `set_config`. `true` makes
  the GUC die at COMMIT/ROLLBACK. We always pass `true`.
- **append-only RLS shape** — read + write policies, no update or
  delete. Combined with FORCE, makes the table append-only at the
  schema layer.
- **`atlas_service_account`** — the BYPASSRLS escape hatch, reachable
  only via `SET LOCAL ROLE` from an `atlas_app` session. No
  production caller in v1.
