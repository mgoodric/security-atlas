# 065 — self-host bundle P0 fixes (slice 037 follow-up)

**Cluster:** Infra / deploy
**Estimate:** 1.5d
**Type:** AFK (P0 follow-up to slice 037)

## Narrative

P0 follow-up to slice 037 (`feat(infra): docker-compose self-host bundle (#037)`, gh#88, merged 2026-05-12 as `42660e9`). The v1.2.0 / v1.3.0 self-host bundle does NOT bring a fresh deployment to a working state. Five distinct bugs surface on the first real bring-up — three are blockers for any fresh install, two block deployments that reuse an external Postgres cluster (the documented "external DB" upgrade path implied by canvas §9 single-VM target).

This slice closes those five gaps so the slice-037 acceptance criteria (AC-1, AC-2, AC-3, AC-4 in particular) actually pass against a fresh checkout, against both the bundled `postgres:16-alpine` and against an externally-provided Postgres 16 instance.

### What was discovered

The bundle was test-driven against the bundled `postgres:16-alpine` container with `trust` auth on the docker network — a configuration where most of these defects are masked. A real deployment against a shared Postgres instance (e.g. an existing homelab `pgvector-16` container on `personal-ai`) surfaces all five:

| #   | Severity                        | File                                                                                               | Line(s)                                                     | Symptom                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| --- | ------------------------------- | -------------------------------------------------------------------------------------------------- | ----------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 1   | **Blocker for ALL deploys**     | `internal/authz/audit.go`                                                                          | 45-96                                                       | Every authenticated request returns `HTTP 500 {"error":"audit log write failed"}`. Bootstrap phase 6 ("upload control bundles") fails 50/50; `api_keys` table stays empty even though atlas logs "bootstrap credential issued". Root cause: `w.pool.Exec(ctx, stmt, ...)` is called OUTSIDE a transaction, so `app.current_tenant` GUC is unset when the INSERT runs and the `decision_audit_log.tenant_write` `WITH CHECK (current_tenant_matches(tenant_id))` RLS policy rejects every row. `internal/tenancy/apply.go:14-16` literally documents this footgun ("outside a transaction the is_local flag is silently inert, RLS sees the GUC as empty") but `audit.go` ignores it. |
| 2   | **Blocker for ALL deploys**     | `deploy/docker/docker-compose.yml`                                                                 | atlas service `depends_on`                                  | Bootstrap phase 5 "waits for atlas /health" but atlas's `depends_on: atlas-bootstrap: condition: service_completed_successfully` won't let atlas start until bootstrap exits. Deadlock — bootstrap times out at 90 attempts, exits 1, atlas never starts. The startup-ordering comment block at the top of the file (`-> atlas starts -> atlas-bootstrap phase 6 uploads control bundles once /health is up`) describes the intended choreography correctly; the depends_on condition contradicts it.                                                                                                                                                                                |
| 3   | **Blocker on bootstrap re-run** | `migrations/sql/20260511000000_init.sql`                                                           | 17 (and other `CREATE TYPE` sites across the migration set) | `psql:...: ERROR: type "control_implementation_type" already exists`. Bootstrap docstring claims "Migrations use IF NOT EXISTS / ON CONFLICT semantics" but the `CREATE TYPE` statements are unguarded, so re-running bootstrap against a partially-migrated DB exits non-zero immediately. Any failure in phases 3-6 strands the deployment with no way to retry.                                                                                                                                                                                                                                                                                                                   |
| 4   | **Blocker on shared Postgres**  | `migrations/bootstrap/01-roles.sql` + `deploy/docker/bootstrap/bootstrap.sh:75-78`                 | role attrs + ALTER ROLE call                                | `ERROR: permission denied to alter role. DETAIL: To change another role's password, the current user must have the CREATEROLE attribute and the ADMIN option on the role.` On a dedicated `postgres:16-alpine` with trust auth, atlas_migrate happens to function as the superuser's stand-in and the ALTER ROLE just works. On a shared cluster the bootstrap dies in phase 2.5.                                                                                                                                                                                                                                                                                                    |
| 5   | **Blocker on shared Postgres**  | `migrations/sql/20260511000000_init.sql` (or a new `20260511000000_extensions.sql` head migration) | top of init                                                 | `seed.sql:67: ERROR: function digest(unknown, unknown) does not exist`. `seed.sql` calls `digest(...)` which requires `pgcrypto`. On the bundled image pgcrypto is pre-enabled by some path; on a shared Postgres it isn't. Same will eventually bite anyone running a dedicated postgres with a non-default base image.                                                                                                                                                                                                                                                                                                                                                             |

### Why one slice (not five)

Each of these blocks the slice-037 acceptance criteria from actually passing. Bundling them keeps the "is the self-host bundle real?" answer in a single PR rather than five interleaved ones, and lets the integration test (acceptance criterion AC-12 below) verify the end-to-end install against both deploy shapes in one CI run. The author may split into sub-PRs internally if review hygiene demands it; the slice closes as one unit when AC-12 passes.

## Threat model (for AC-1)

| Actor                                                        | Pre-fix capability                                                                                                                                                       | Post-fix capability                                                                                                                                               |
| ------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Any authenticated client (including the bootstrap container) | Every API call returns HTTP 500; `api_keys` table stays empty across the platform; the platform is reachable on /health but unusable for any tenant-scoped read or write | Authenticated requests succeed; audit rows land in `decision_audit_log` with the correct `tenant_id`; `api_keys` records the issued bootstrap + admin credentials |
| Bootstrap container                                          | Phase 6 fails 50/50 on every fresh install; 0 control bundles loaded                                                                                                     | Phase 6 succeeds; the seeded 50 SOC 2 control bundles are persisted to `controls`                                                                                 |

This is severity P0 because the platform is shipped with this bug in v1.3.0 and any new self-host deployment is non-functional past startup.

## Acceptance criteria

### AC-1 to AC-3 — audit-writer tenant context (bug #1)

- [ ] AC-1: `internal/authz/audit.go` `(*AuditWriter).Write` wraps the INSERT in a `BeginTx` + `tenancy.ApplyTenant(ctx, tx)` + `tx.Exec` + `tx.Commit` block — same pattern documented in `internal/tenancy/apply.go`. `defer tx.Rollback(ctx)` handles error paths.
- [ ] AC-2: New integration test `TestAuditWriter_TenantGUCApplied` against a real Postgres instance with RLS enforced on `decision_audit_log` proves an INSERT succeeds when the test sets up a tenant ctx, and fails when it does not (asserting the WITH CHECK is actually triggered). The test must not use `atlas_migrate` (BYPASSRLS) — it must use `atlas_app` so RLS is enforced.
- [ ] AC-3: Bootstrap credential issuance (`cmd/atlas/main.go:290` and the surrounding "fixed-token admin credential issued" path) writes successfully to `api_keys` against an RLS-enforced shared cluster. If the call path was relying on the same silently-failing `pool.Exec` shape, it gets the same `BeginTx + ApplyTenant` treatment OR is explicitly switched to the `atlas_migrate` BYPASSRLS pool with a doc comment explaining why startup-time inserts predate any request tenant context.

### AC-4 to AC-5 — bootstrap/atlas startup ordering (bug #2)

- [ ] AC-4: `deploy/docker/docker-compose.yml` atlas service `depends_on.atlas-bootstrap` is changed from `condition: service_completed_successfully` to `condition: service_started`, with a code-comment block above pointing to the bootstrap.sh phase-5+6 reason. The atlas healthcheck's `start_period` is bumped from `20s` to at least `120s` so atlas's restart loop has room to converge while bootstrap is still applying migrations.
- [ ] AC-5: A short-form alternative ("split bootstrap into pre-atlas + post-atlas services") is evaluated and documented in a code comment if rejected. If accepted, implement it instead: a `atlas-bootstrap-pre` service runs phases 1-4 to `service_completed_successfully`, atlas starts after that, and a separate `atlas-bootstrap-post` service runs phases 5-6 against the live atlas.

### AC-6 to AC-7 — migration idempotency (bug #3)

- [ ] AC-6: Every `CREATE TYPE foo AS ENUM (...)` in `migrations/sql/*.sql` is rewritten to a guarded form. Pick one canonical pattern and apply uniformly:
  ```sql
  DO $$ BEGIN
      CREATE TYPE control_implementation_type AS ENUM ('preventive','detective','corrective');
  EXCEPTION WHEN duplicate_object THEN NULL; END $$;
  ```
  Same treatment for any `CREATE FUNCTION` / `CREATE OPERATOR` / `CREATE CAST` that lacks `OR REPLACE` or `IF NOT EXISTS`. Tables, indexes, and policies are already guarded; types are the conspicuous gap.
- [ ] AC-7: New CI integration test `TestBootstrap_IsIdempotent` brings up the docker-compose bundle, lets it complete, then re-runs `docker compose run --rm atlas-bootstrap` and asserts exit code 0 and row-counts identical (no duplicate seed rows, no migration errors).

### AC-8 to AC-9 — bootstrap role permissions on shared Postgres (bug #4)

- [ ] AC-8: `migrations/bootstrap/01-roles.sql` grants atlas_migrate enough privilege to manage atlas_app's password on its own:
  ```sql
  DO $$ BEGIN
      IF NOT (SELECT rolcreaterole FROM pg_roles WHERE rolname='atlas_migrate') THEN
          ALTER ROLE atlas_migrate CREATEROLE;
      END IF;
  END $$;
  GRANT atlas_app TO atlas_migrate WITH ADMIN OPTION;
  ```
  The bootstrap docstring is updated to flag that this widens atlas_migrate beyond pure DDL — atlas_migrate is now permitted to manage atlas_app's password, but not to create arbitrary new roles in production (the CREATEROLE grant is scoped at the DB cluster but the only role atlas_migrate has admin option on is atlas_app, so it can't escalate beyond that single role).
- [ ] AC-9: New integration test `TestBootstrap_AgainstSharedPostgres` runs the bootstrap container against a Postgres where atlas_migrate is NOT a superuser, with a pre-existing atlas_migrate role and password — verifying the ALTER ROLE atlas_app step succeeds end-to-end. The current `postgres:16-alpine` test path likely uses superuser-via-trust which masked this; the new test deliberately uses scram-sha-256 + a non-superuser atlas_migrate.

### AC-10 to AC-11 — pgcrypto + uuid-ossp extensions (bug #5)

- [ ] AC-10: A new head migration `migrations/sql/20260511000000_extensions.sql` (or prepended into the existing init migration) issues `CREATE EXTENSION IF NOT EXISTS pgcrypto;` and `CREATE EXTENSION IF NOT EXISTS "uuid-ossp";` (if uuid-ossp is required — confirm via grep). Bootstrap runs this BEFORE `seed.sql` so the digest() call in seed.sql has the function it needs.
- [ ] AC-11: `deploy/docker/docker-compose.yml` postgres service is unchanged (extension creation is now a migration, not a side effect of the postgres image's init scripts), so the bundle works against both `postgres:16-alpine` AND `pgvector/pgvector:pg16` AND a vanilla shared Postgres 16 cluster.

### AC-12 — end-to-end integration

- [ ] AC-12: A new CI job `test-self-host-bundle` brings up the self-host bundle in TWO modes against a fresh checkout and asserts every slice-037 acceptance criterion (AC-1 through AC-7 from slice 037) passes — including the 4-hour-to-first-evidence demo path — for BOTH:
  1. **Bundled postgres**: `docker compose up -d` as documented in `deploy/docker/README.md`
  2. **External postgres**: a Postgres 16 sidecar started in the same compose with `trust` auth disabled, atlas_migrate + atlas_app pre-created with passwords (the "shared cluster" shape)
     The job fails if /health doesn't return 200, if any of the 50 control bundles fail to upload, if `api_keys` doesn't end up with the bootstrap + fixed-token rows, or if a fresh re-run of `atlas-bootstrap` against a populated DB returns non-zero.

## Constitutional invariants honored

- **Invariant 6 (RLS at DB layer):** AC-1/2/3 close the only path that bypassed RLS in production code. The audit writer now respects the same `BeginTx + ApplyTenant` discipline `internal/tenancy/apply.go` documents.
- **Slice 037 acceptance criterion AC-1 (5-minute bring-up):** AC-4/5 make this achievable instead of "deadlocks every time".
- **Slice 037 acceptance criterion AC-3 (default user can sign in):** AC-1 indirectly fixes this — currently the sign-in path 500s on the post-auth audit write, even if the user submits the right password.
- **Anti-pattern rejected:** "application code is not the trust boundary" — the audit-writer fix removes the last code path where RLS depended on the writer remembering to enter a transaction.

## Canvas references

- `Plans/canvas/09-tech-stack.md` (single-VM deployment target — shared-DB shape is implied for the homelab/SMB segment)
- `CLAUDE.md` Invariant 6
- `internal/api/tenancymw/middleware.go` (docstring: "every other handler MUST inherit from this middleware" — the audit writer is the documented exception that was never fixed)
- `internal/tenancy/apply.go` (lines 14-16 — the footgun this slice closes)
- `docs/issues/037-docker-compose-self-host.md` (the slice this is a P0 follow-up to)

## Dependencies

- #037 (merged) — the self-host bundle this slice fixes
- #033 (merged) — `tenancymw.Middleware` is the GUC setter the audit fix composes with
- #034 (merged) — `apikeystore.Store` is where the startup credential-issuance path lands

## Anti-criteria (P0 — block merge)

- Does NOT widen atlas_app's privileges (atlas_app stays `NOSUPERUSER NOBYPASSRLS` — the fix runs through proper RLS, not by escaping it)
- Does NOT add a "BYPASSRLS-only audit writer" pool as a workaround — the fix wires the existing audit writer to use the existing tenancy machinery correctly
- Does NOT change the public API surface — no new env vars, no new endpoints, no breaking changes for clients already on v1.3.0
- Does NOT regress slice 033 RLS guarantees — every existing RLS integration test continues to pass; new tests are additive
- Does NOT skip AC-12 in favor of unit tests only — the bundle must be exercised end-to-end against BOTH deploy shapes, in CI, on every PR that touches `deploy/docker/`, `migrations/`, or `internal/authz/`
- Does NOT bake DB credentials into the published bootstrap image — the .env-based credential flow stays
- Does NOT use vendor-prefixed tokens in test fixtures (neutral `test-*` only)

## Skill mix (3–5)

- pgx transactions + RLS GUC propagation (Go)
- Postgres role/privilege model (CREATEROLE + WITH ADMIN OPTION semantics)
- docker compose dependency conditions (`service_started` vs `service_completed_successfully` vs split-service patterns)
- Migration idempotency patterns (DO blocks with EXCEPTION WHEN duplicate_object)
- CI matrix for self-host integration testing (bundled + shared deploy shapes)

## Notes for the implementing agent

Detailed bug-by-bug findings with file:line citations and proposed code shapes live in the operator-facing notes captured during the v1.3.0 first-deploy session (`~/.claude/MEMORY/WORK/20260514-064726_security-atlas-unraid-deploy/UPSTREAM-BUGS.md`). The five bugs there map 1:1 to bugs #1-#5 in this slice; the AC numbering aligns with that document for cross-reference convenience.
