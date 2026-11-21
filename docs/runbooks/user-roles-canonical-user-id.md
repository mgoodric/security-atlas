# Runbook — `user_roles` rows with a non-canonical uuid `user_id` (#2661)

**Scope:** an upgrade that stops at
`20261280000000_user_roles_canonical_user_id.sql` with:

```
ERROR:  user_roles: N row(s) store a non-canonical uuid user_id; see docs/runbooks/user-roles-canonical-user-id.md (#2661)
```

**Audience:** the self-host operator running the upgrade.

## Why the migration stops

The migration adds a CHECK that a `user_roles.user_id` which reads as a uuid
is stored in canonical form: lowercase, hyphenated 8-4-4-4-12. The bulk role
query joins `users` on `u.id::text = ur.user_id`, and `u.id::text` is always
canonical. A row stored as `A0EEBC99-...`, `{a0eebc99-...}`,
`urn:uuid:a0eebc99-...` or `a0eebc999c0b...` never joins, so the
deactivation guard would miss a disabled user's role. No shipped write path
stores such a row, so the CHECK should apply cleanly. If it doesn't, some
row was written by hand or by an older build, and the migration refuses to
start until it's fixed.

Values that aren't uuids are unaffected: `key_...`, `bootstrap-admin`,
`oauth_client:<id>` and `user:<uuid>`.

## Nothing to clear before a re-run

`deploy/docker/bootstrap/migrate.sh` (`just migrate-up`, and the compose
`atlas-migrate` service) applies each file and its `schema_migrations` row
in one transaction. A failed migration leaves no ledger row and no partial
schema, so there is no dirty version to clear. Fix the rows, then re-run the
same command.

## Fix the rows

Run as the migrate/owner role (`$DATABASE_URL`), which bypasses RLS and sees
every tenant: `psql "$DATABASE_URL" -v ON_ERROR_STOP=1`. Everything runs
in one transaction. Read the review `SELECT` output before you type `COMMIT`,
and use `ROLLBACK` instead if it lists anything you didn't expect.

For each offending row the snippet works out the canonical id by stripping
`urn:uuid:`, braces and hyphens and casting the 32 hex digits through
`::uuid`. A plain `lower()` would leave braced, `urn:uuid:` and 32-hex
values non-canonical. Then:

1. It writes a canonical row for each `(tenant_id, user_id, role)` that
   doesn't already have one, keeping the earliest grant's `granted_at`,
   `granted_by` and `origin`. If the canonical row already exists, it is
   kept as it is, and so are duplicate spellings of the same id.
2. It copies the row's `user_role_group_sources` entries to the canonical
   row. Their FK has no `ON UPDATE`, which is why the snippet writes new
   rows instead of updating `user_id` in place.
3. It deletes the non-canonical rows, and their source entries cascade.

```sql
BEGIN;

CREATE TEMP TABLE user_roles_2661 ON COMMIT DROP AS
SELECT tenant_id, user_id, role, granted_at, granted_by, origin,
       replace(regexp_replace(regexp_replace(lower(user_id), '^(urn:uuid:)?\{?', ''), '\}$', ''), '-', '')::uuid::text AS canonical
  FROM user_roles
 WHERE regexp_replace(regexp_replace(lower(user_id), '^(urn:uuid:)?\{?', ''), '\}$', '')
           ~ '^(-*[0-9a-f]-*){32}$'
   AND user_id !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';

-- Review: every row that will be rewritten, and what it becomes.
SELECT tenant_id, user_id, canonical, role
  FROM user_roles_2661
 ORDER BY tenant_id, canonical, role, user_id;

-- 1. A canonical row for every (tenant, user, role) that has none.
INSERT INTO user_roles (tenant_id, user_id, role, granted_at, granted_by, origin)
SELECT DISTINCT ON (tenant_id, canonical, role)
       tenant_id, canonical, role, granted_at, granted_by, origin
  FROM user_roles_2661
 ORDER BY tenant_id, canonical, role, granted_at
    ON CONFLICT (tenant_id, user_id, role) DO NOTHING;

-- 2. Carry group-role sources over to the canonical row.
INSERT INTO user_role_group_sources (tenant_id, user_id, role, idp_config_id, granted_at)
SELECT DISTINCT ON (s.tenant_id, f.canonical, s.role, s.idp_config_id)
       s.tenant_id, f.canonical, s.role, s.idp_config_id, s.granted_at
  FROM user_role_group_sources s
  JOIN user_roles_2661 f
    ON f.tenant_id = s.tenant_id AND f.user_id = s.user_id AND f.role = s.role
 WHERE NOT EXISTS (
       SELECT 1 FROM user_role_group_sources c
        WHERE c.tenant_id = s.tenant_id
          AND c.user_id = f.canonical
          AND c.role = s.role
          AND c.idp_config_id IS NOT DISTINCT FROM s.idp_config_id)
 ORDER BY s.tenant_id, f.canonical, s.role, s.idp_config_id, s.granted_at;

-- 3. Remove the non-canonical rows; their source rows cascade.
DELETE FROM user_roles ur
 USING user_roles_2661 f
 WHERE ur.tenant_id = f.tenant_id AND ur.user_id = f.user_id AND ur.role = f.role;

COMMIT;
```

Then re-run the migration (`just migrate-up`, or restart the
`atlas-migrate` service).

`TestUserRolesCanonicalUserID` in
`internal/db/user_roles_canonical_user_id_integration_test.go` runs the
`sql` block above, read from this file. It seeds upper-case, braced,
`urn:uuid:` and 32-hex rows, some with a canonical twin, a duplicate
spelling or a group source, then checks that the migration applies and the
rows come out canonical. If you edit the snippet, run that test.
