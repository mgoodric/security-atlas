# 065 — self-host bundle P0 fixes — decisions log

Slice 065 is `Type: AFK`. This log records the subjective build-time
judgment calls made while fixing the five first-deploy bugs, in the
JUDGMENT-slice format (Decisions made · Revisit once in use · Confidence)
so the maintainer can re-evaluate them once the bundle is in real use. It
does NOT block merge.

## Decisions made

### 1. Transaction idiom — explicit `pool.Begin` + `defer Rollback` + `Commit`, not `pgx.BeginTxFunc`

**Options considered:**

- **(A)** The issue's AC-1 prose: `BeginTx` + `tenancy.ApplyTenant` +
  `tx.Exec` + `tx.Commit` with `defer tx.Rollback`.
- **(B)** `pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{}, func(tx) error {...})`
  — the closure form, auto-commit on nil return.

**Chosen: (A), realised via the `internal/risk/store.go` `inTx` house
shape.** The ~40 stores in the repo split roughly between the closure
form and the explicit `Begin` + `defer Rollback` + `Commit` form;
`internal/risk/store.go`'s `inTx` helper is the explicit form and is the
closest precedent to a single-statement writer. The explicit form also
maps 1:1 onto the issue's AC-1 wording, so the diff reads exactly as the
AC describes. The deferred `Rollback` after a successful `Commit` is a
documented no-op in pgx.

**Confidence: high.** Both forms are idiomatic in this repo; this one
matches the AC text and the nearest house precedent.

### 2. pgcrypto delivered as a discrete head migration, not prepended into `_init.sql`

**Chosen:** a new `migrations/sql/20260511000000_extensions.sql` (+
`.down.sql`) rather than prepending `CREATE EXTENSION` into
`_init.sql`. AC-10 explicitly names the file; `_init.sql` is the sqlc
source-of-truth and keeping `CREATE EXTENSION` out of it avoids sqlc ever
parsing it; the discrete file is trivially reversible. Both bootstrap.sh
and ci.yml iterate `migrations/sql/*.sql` in lexical order and
`extensions` < `init`, so it runs first — verified locally against
`postgres:16-alpine`.

Only `pgcrypto` is created — `digest()` (used by `seed.sql`) needs it;
`gen_random_uuid()` is core Postgres 13+ and needs nothing; `uuid-ossp`
has no call site (repo-wide grep).

**Confidence: high.** Verified the ordering and the clean apply locally.

### 3. `_extensions.down.sql` does `DROP EXTENSION IF EXISTS pgcrypto`

**Chosen:** a real drop, not a no-op comment. Nothing in the schema
holds a DDL-time dependency on the extension (`digest` /
`gen_random_uuid` are DML-time calls), and the down migration runs LAST
in the CI reverse-order round-trip, by which point every later table and
enum is already gone. Matches the per-migration `.down.sql` convention.

**Confidence: high.**

### 4. CREATEROLE on `atlas_migrate` — conditional `DO` block; shared-cluster operators pre-grant

**The subtlety:** a non-superuser role cannot grant ITSELF `CREATEROLE`.
On a dedicated `postgres:16-alpine` container with trust auth,
`atlas_migrate` effectively stands in for the superuser and
`ALTER ROLE atlas_migrate CREATEROLE` just works. On a genuinely shared
cluster where `atlas_migrate` is pre-created as a non-superuser, that
`ALTER` is itself permission-denied.

**Chosen:** the `DO` block in `01-roles.sql` is conditional —
`IF NOT rolcreaterole THEN ALTER ROLE atlas_migrate CREATEROLE`. When the
shared-cluster operator has already granted `CREATEROLE` (documented as a
required one-time cluster-admin step in the file header and exercised by
the CI `external` mode), the `ALTER` is skipped and only the
`GRANT atlas_app TO atlas_migrate WITH ADMIN OPTION` runs — which
`atlas_migrate` CAN perform on itself once it holds `CREATEROLE`. If the
operator has NOT pre-granted it, the `ALTER` raises a clear
permission-denied error, which is the correct signal.

`atlas_app` is unchanged — still `NOSUPERUSER NOBYPASSRLS` (anti-criterion
P0). The widening of `atlas_migrate` is scoped: the only role it holds
ADMIN OPTION on is `atlas_app`.

**Confidence: high.** The CI `external`-mode job exercises exactly this
pre-created-non-superuser path.

### 5. AC-3 — bootstrap credential issuance needs NO change; it is in-memory

**Finding:** `IssueBootstrapCredential` /
`IssueBootstrapFixedAdminCredential` (`cmd/atlas/main.go`,
`internal/api/server.go`) write into the **in-memory** `credstore.Store`,
not the `api_keys` table — they never touch the DB pool, so they cannot
hit the `pool.Exec`-outside-a-transaction RLS-bypass that bug #1 fixed.
The slice-037 symptom "`api_keys` stays empty on a fresh install" was a
downstream effect of bug #1: the audit-writer 500 blocked bootstrap
phase 6 (control-bundle upload), and that authenticated upload path is
what actually persists to `api_keys`. With the audit writer fixed,
phase 6 completes and `api_keys` populates.

**Chosen:** document the finding with a comment in `cmd/atlas/main.go`;
make no code change to the issuance path. AC-3's "OR is explicitly
switched to the BYPASSRLS pool with a doc comment" escape hatch is moot —
there is no DB write to switch.

**Confidence: high.** Traced the full call path through `credstore.go`.

### 6. Migration idempotency — a `schema_migrations` ledger in `bootstrap.sh`, not blanket `IF NOT EXISTS`

**The discovery:** AC-6 states "Tables, indexes, and policies are already
guarded; types are the conspicuous gap." Local verification proved that
**false** — re-applying the forward migrations against an already-migrated
DB fails on the first unguarded `CREATE TABLE` (`relation "frameworks"
already exists`), not just on `CREATE TYPE`. There is no migration-runner
or `schema_migrations` table in the repo; `bootstrap.sh` phase 2 was a
bare `for f in *.sql; do psql -f` loop. So guarding only `CREATE TYPE`
(literal AC-6) would leave AC-7's `TestBootstrap_IsIdempotent` /
`docker compose run --rm atlas-bootstrap` re-run still failing on
`CREATE TABLE`.

**Options considered:**

- **(A)** Guard only `CREATE TYPE` (literal AC-6). Leaves AC-7 failing.
- **(B)** Add `IF NOT EXISTS` / `ADD COLUMN IF NOT EXISTS` etc. across all
  31 migration files. Massive, fragile diff; fights `_init.sql`'s
  sqlc-source-of-truth role.
- **(C)** Add a `schema_migrations` ledger to `bootstrap.sh`: record each
  applied migration's basename, skip it on re-run.

**Chosen: (C), plus the (A) `CREATE TYPE` guards as a complement.** The
ledger is the correct engineering fix and is exactly what `bootstrap.sh`'s
own history anticipated (the justfile comment: "A real migration runner
lands when slice N adds the second migration" — slice 065 is well past
that). The `CREATE TYPE` guards are still applied and still matter: they
cover the *partial-failure recovery* path — a migration that errored
AFTER creating its enums but BEFORE its `schema_migrations` row was
written will be retried, and the guarded `CREATE TYPE` lets it get past
the enums it already created. The ledger row + the migration DDL are
written in one `--single-transaction` psql invocation so a mid-apply
failure leaves no ledger row.

`schema_migrations` is a plain unversioned table owned by `atlas_migrate`,
no `tenant_id`, no RLS — operational bookkeeping, the same category as a
versioning tool's metadata table. It is created only by `bootstrap.sh`;
CI's own raw-psql migration loops are unaffected (CI applies to a fresh
DB and its down-then-up round-trip drops everything, so the `CREATE TYPE`
guards alone keep that path green — verified locally).

**Confidence: medium-high.** The ledger logic is verified locally (run 1
applies + records 33; run 2 skips 33, applies 0; down-then-up still
clean). Lower than "high" only because this is a scope expansion beyond
AC-6's literal text — flagged prominently for maintainer review.

### 7. Scope expansion — `db_resolver.go` fixed alongside `audit.go`

**Finding:** `internal/authz/db_resolver.go`'s `DBRolesResolver.RolesFor`
has the **identical** bug class as `audit.go`: it queries the
RLS-enforced `atlas_app` pool with `pool.Query` OUTSIDE a transaction, so
the `app.current_tenant` GUC is empty and the `user_roles` `tenant_read`
RLS policy matches nothing — every DB-backed role lookup silently returns
zero roles. Both shipped in slice 035. `internal/authz`'s integration
tests were never wired into CI's integration job, so this has been
latently broken since.

**Chosen:** fix `db_resolver.go` with the same `Begin` + `ApplyTenant` +
`tx.Query` pattern. It is the same one-line-class bug in the same
package; the slice's whole purpose is "make a fresh deploy functional"
and DB-backed authz being broken means authenticated authorization is
broken on every deploy; and anti-criterion ISC-A4 ("every existing RLS
integration test still passes") cannot be honoured while
`TestAuthzDBRolesResolver` is red. Not a regression I introduced — it was
already red on `main` — but knowingly shipping the slice with it left
half-fixed would be wrong.

**Confidence: high.** `TestAuthzDBRolesResolver` and the audit tests all
pass post-fix.

### 8. CI `test-self-host-bundle` job — `matrix`, no slice-061 stub sibling

**Chosen:** the new CI job uses a `matrix` over `[bundled, external]`,
which makes GitHub post per-mode check names (`... (bundled)` /
`... (external)`). The slice-061 stub-sibling pattern relies on a single
fixed check name, so it cannot mirror a matrix job. The job is
intentionally NOT yet added to `.github/branch-protection.json`'s
required-checks list, so a docs-only PR simply skips it (via the
`changes.code` `if:` gate) with no "waiting for status" hang. A
follow-up can promote it to required and add matrix-named stubs once it
has a few green runs.

**Confidence: medium.** The job is correct; the "promote to required
later" deferral is the soft part.

### 9. Existing-test read-back hygiene — added a `tenantTx` test helper

**Chosen:** `internal/authz/matrix_integration_test.go`'s existing tests
(`TestAuthzAuditLog_BothOutcomesPersist`, `..._AppendOnlyRLS`) read back
`decision_audit_log` via bare `appPool.QueryRow` — the same
outside-a-transaction RLS-blind pattern. Added a `tenantTx(t, ctx, pool)`
helper that opens a tx and applies the GUC, and routed those read-backs
through it. The helper deliberately does NOT register rollback via
`t.Cleanup` — cleanups run AFTER the test's own `defer pool.Close()`, and
`Close()` blocks forever on a still-open tx (hit this exact deadlock once
during development); callers `defer tx.Rollback(ctx)` instead.

**Confidence: high.** Caught and fixed the `pool.Close()` deadlock; full
suite is green.

## Revisit once in use

- **Decision 6 (ledger):** if a future slice adopts a real migration tool
  (Atlas/golang-migrate), the `schema_migrations` table created here
  should be reconciled with — or handed over to — that tool. The shape
  (`filename` PK + `applied_at`) is deliberately tool-agnostic.
- **Decision 4 (CREATEROLE):** the shared-cluster pre-grant requirement is
  documented in the `01-roles.sql` header and exercised by CI, but it is
  not yet in `docs/SELF_HOSTING.md`. Add it there when the external-DB
  upgrade path gets a dedicated docs section.
- **Decision 8 (CI job not required):** promote `test-self-host-bundle` to
  a required check once it has a stable green history; add the
  matrix-named stub siblings at that point.
- **Decision 7 (db_resolver scope):** the broader question — "why were
  `internal/authz` integration tests never in the CI integration job?" —
  is worth a follow-up. This slice adds `./internal/authz/...` to the CI
  integration list so the regression cannot recur silently.
