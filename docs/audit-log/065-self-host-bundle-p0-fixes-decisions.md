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

### 4. Shared-cluster `atlas_migrate` — one-time `BYPASSRLS + CREATEROLE` grant; conditional `DO` block

**Original framing (CREATEROLE only) was incomplete — corrected during
the CI-greening follow-up.** A non-superuser role cannot grant ITSELF
`CREATEROLE` _or_ `BYPASSRLS`. On a dedicated `postgres:16-alpine`
container, `01-roles.sql` creates `atlas_migrate` as `LOGIN BYPASSRLS`
directly. On a genuinely shared cluster, `atlas_migrate` is pre-created
by the operator as a plain non-superuser and the cluster admin must
widen it.

**The puzzle the CI `external` mode surfaced:** the first cut of the CI
harness pre-created `atlas_migrate` as `NOSUPERUSER` and granted it only
`CREATEROLE`. `01-roles.sql` then failed — but NOT on
`GRANT atlas_app TO atlas_migrate WITH ADMIN OPTION` (that statement is
fine: `atlas_migrate` creates `atlas_app` in the same `DO` block and is
therefore implicitly its admin). It failed at
`CREATE ROLE atlas_service_account ... BYPASSRLS`: PG16 only lets a role
that itself has the `BYPASSRLS` attribute create another `BYPASSRLS`
role. The error surfaces as the misleading `permission denied to create
role`, with `DETAIL: Only roles with the BYPASSRLS attribute may create
roles with the BYPASSRLS attribute`.

**Chosen:** the documented one-time cluster-admin grant is
`ALTER ROLE atlas_migrate BYPASSRLS CREATEROLE` — both attributes, in
one statement. This is **not** a privilege widening beyond the
dedicated-container case: it makes the shared-cluster `atlas_migrate`
_identical_ to the dedicated-container `atlas_migrate`, which is
`LOGIN BYPASSRLS` by design (the self-host bootstrap connects as
`atlas_migrate` for the cross-tenant boot-time writes — see
`bootstrap.sh`'s header). `CREATEROLE` then lets it create `atlas_app`
and hold implicit ADMIN on it so `bootstrap.sh` phase 2.5's
`ALTER ROLE atlas_app PASSWORD` succeeds.

The `DO` block in `01-roles.sql` stays conditional —
`IF NOT rolcreaterole THEN ALTER ROLE atlas_migrate CREATEROLE`. When the
operator has pre-granted `BYPASSRLS + CREATEROLE` (documented in the
`01-roles.sql` header, exercised by the CI `external` mode), the `ALTER`
is skipped and only the `WITH ADMIN OPTION` grant runs. If the operator
pre-granted nothing, the conditional `ALTER` raises a clear
permission-denied error — the correct signal.

`atlas_app` is unchanged — still `NOSUPERUSER NOBYPASSRLS` (anti-criterion
P0; verified `rolbypassrls = f`, `rolsuper = f` post-fix). `atlas_migrate`
does NOT become superuser (`rolsuper = f` post-fix). The widening is
scoped: the only role `atlas_migrate` holds ADMIN OPTION on is
`atlas_app`.

**Why the soundest of the brief's three options:** option (b)
"restructure so `atlas_migrate` creates `atlas_app`" is what already
happens and was never the failure. Option (a)/(c) "have the harness's
cluster-admin step grant more" is the right shape — and the _minimal_
correct "more" is exactly `BYPASSRLS`, the attribute `01-roles.sql`
itself already assigns `atlas_migrate` on a dedicated cluster. No new
asymmetry between the two deploy shapes; no `atlas_app` change; no
superuser.

**Confidence: high.** Reproduced locally against `postgres:16-alpine`:
with the pre-created `NOSUPERUSER` `atlas_migrate` granted only
`CREATEROLE`, `01-roles.sql` exits 3 at the `atlas_service_account`
`CREATE ROLE`; granted `BYPASSRLS CREATEROLE` it runs to exit 0, the
subsequent `ALTER ROLE atlas_app PASSWORD` as `atlas_migrate` succeeds,
a re-run of `01-roles.sql` is idempotent, and `pg_roles` confirms
`atlas_app` = `NOSUPERUSER NOBYPASSRLS`, `atlas_migrate` = not superuser.

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
cover the _partial-failure recovery_ path — a migration that errored
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

### 10. Bundled-mode first boot — `01-roles.sql` via initdb + `POSTGRES_HOST_AUTH_METHOD` default

**Discovered during the CI-greening follow-up.** The `bundled` matrix
mode of `test-self-host-bundle.sh` failed with `atlas-bootstrap` looping
60× on "Postgres not reachable" then exiting 1. There were TWO stacked
root causes — both in the bundled deploy shape, neither exercised before
this slice added the end-to-end harness.

**Cause A — role-bootstrap chicken-and-egg (the primary blocker).**
`bootstrap.sh` connects to Postgres ONLY as `atlas_migrate`
(`DATABASE_URL` = `DATABASE_URL_MIGRATE`). On a fresh bundled `pg-data`
volume that role does not exist yet — and nothing creates it before
`bootstrap.sh` runs, because `bootstrap.sh` is itself what runs
`01-roles.sql`, and it tries to do so _as_ `atlas_migrate`. Phase 1's
wait-for-Postgres loop therefore fails every attempt with `role
"atlas_migrate" does not exist` and times out. `atlas-bootstrap` cannot
create the role it needs in order to connect.

**Cause B — auth method.** Even once the role exists, the bundled
`DATABASE_URL_MIGRATE` is **password-less** (matching `.env.example`,
which documents `atlas_migrate` "authenticates via trust on the container
network"), but `postgres:16-alpine` with `POSTGRES_PASSWORD` set and
`POSTGRES_HOST_AUTH_METHOD` **unset** writes `host all all all
scram-sha-256` into `pg_hba.conf`, so the cross-container connection is
rejected with `fe_sendauth: no password supplied`. The
"trust-on-the-docker-network auth" the compose header + `.env.example`
both describe was documented but never wired.

**Options considered for Cause A:**

- **(A1)** Give `bootstrap.sh` a superuser DSN and run `01-roles.sql`
  as `postgres`. Rejected: the `atlas-bootstrap` compose service is
  deliberately not handed superuser credentials, and adding them widens
  its blast radius for every boot.
- **(A2)** Have `bootstrap.sh` connect as `postgres` for phases 1–2
  only. Rejected: same credential-widening problem; also a larger
  `bootstrap.sh` restructure than a targeted fix should make.
- **(A3)** Mount `migrations/bootstrap/01-roles.sql` into the postgres
  container's `/docker-entrypoint-initdb.d/`. The postgres image runs
  every file there ONCE at cluster init as the superuser — exactly the
  right time and privilege to create the three roles. `01-roles.sql` is
  fully `IF NOT EXISTS`-guarded, so `bootstrap.sh` phase 2 re-running it
  as `atlas_migrate` is a clean no-op. **Chosen.**

**Chosen: A3 + (for Cause B) `POSTGRES_HOST_AUTH_METHOD:
${POSTGRES_HOST_AUTH_METHOD:-trust}` on the postgres service.** Both
needs no schema change and no new credential. The compose `postgres`
service gains a `${PG_INITDB_ROLES:-../../migrations/bootstrap/01-roles.sql}`
bind-mount into `/docker-entrypoint-initdb.d/01-roles.sql`, so the
**shipped bundle is self-bootstrapping**. Both knobs are overridable per
deploy shape, and the slice-065 harness drives them via `.env.test`:

- `bundled`: `POSTGRES_HOST_AUTH_METHOD=trust`,
  `PG_INITDB_ROLES=../../migrations/bootstrap/01-roles.sql` (the compose
  default — roles created at init, trust auth on the docker network).
- `external`: `POSTGRES_HOST_AUTH_METHOD=` (empty → the postgres image
  falls back to `scram-sha-256`), `PG_INITDB_ROLES=/dev/null` (an empty
  no-op initdb script). This keeps the `external` mode's test premise
  intact: the harness's _own_ `CREATE ROLE atlas_migrate ... NOSUPERUSER`
  step is what creates the role, exactly as a shared-cluster admin would.

The harness writes both into `.env.test` rather than passing inline
`VAR=… docker compose` prefixes so EVERY compose invocation in a run —
the external-mode `up -d postgres`, the full-bundle `up -d --build`, the
idempotency `run --rm`, the failure-log `logs`, the teardown `down` —
sees a consistent value via `--env-file`. Shared-cluster operators do
not run the bundled `postgres` service at all, so neither knob affects
them.

**Confidence: high.** Reproduced locally against `postgres:16-alpine`:
(a) a fresh DB with `01-roles.sql` in `/docker-entrypoint-initdb.d/`
creates all three roles at init with the correct attributes
(`atlas_migrate` BYPASSRLS+CREATEROLE, `atlas_app` NOSUPERUSER
NOBYPASSRLS) and `atlas_migrate` can then connect password-less over the
docker network under `trust`; (b) `/dev/null` mounted as the initdb
script is a clean no-op (zero `atlas%` roles created); (c)
`POSTGRES_HOST_AUTH_METHOD` unset → scram (rejects the password-less
connection), `=trust` → accepts it, `=` empty → scram — i.e. external
mode's auth posture is unchanged.

## Revisit once in use

- **Decision 6 (ledger):** if a future slice adopts a real migration tool
  (Atlas/golang-migrate), the `schema_migrations` table created here
  should be reconciled with — or handed over to — that tool. The shape
  (`filename` PK + `applied_at`) is deliberately tool-agnostic.
- **Decision 4 (BYPASSRLS + CREATEROLE):** the shared-cluster pre-grant
  requirement (`ALTER ROLE atlas_migrate BYPASSRLS CREATEROLE`) is
  documented in the `01-roles.sql` header and exercised by CI, but it is
  not yet in `docs/SELF_HOSTING.md`. Add it there when the external-DB
  upgrade path gets a dedicated docs section.
- **Decision 10 (initdb roles + `POSTGRES_HOST_AUTH_METHOD`):** the
  bundled bundle now (a) mounts `01-roles.sql` into the postgres
  container's `/docker-entrypoint-initdb.d/` so the roles exist before
  `bootstrap.sh` connects, and (b) defaults the postgres container to
  `trust` auth on the docker network — which is what the password-less
  `.env.example` `DATABASE_URL_MIGRATE` always implicitly required. When
  the external-DB upgrade path gets a docs section, document that
  operators pointing at a non-bundled cluster set a real
  `DATABASE_URL_MIGRATE` password, pre-create the roles themselves, and
  simply do not run the bundled `postgres` service (so neither
  `PG_INITDB_ROLES` nor `POSTGRES_HOST_AUTH_METHOD` applies to them). If
  a future slice adds a real migration runner, fold the initdb
  role-bootstrap into its first-run step.
- **Decision 8 (CI job not required):** promote `test-self-host-bundle` to
  a required check once it has a stable green history; add the
  matrix-named stub siblings at that point.
- **Decision 7 (db_resolver scope):** the broader question — "why were
  `internal/authz` integration tests never in the CI integration job?" —
  is worth a follow-up. This slice adds `./internal/authz/...` to the CI
  integration list so the regression cannot recur silently.
