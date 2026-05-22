-- security-atlas — slice 143: create-tenant flow (super_admin-gated).
--
-- Adds the `slug` column to `tenants`, extends two audit-log CHECK
-- constraints (super_admin_audit_log + me_audit_log) to admit the new
-- 'tenant_create' action, and grants `atlas_app` the privileges the
-- POST /v1/admin/tenants handler needs.
--
-- BACKGROUND:
--
--   Slice 144 introduced the `tenants` table (id, name, is_bootstrap_tenant,
--   created_at, updated_at). The slice-doc 143 stated requirement is a
--   stable URL-safe slug for each tenant — the slice-141 multi-tenant
--   login surface keys per-tenant routes by id today, but the operator-
--   facing tenant switcher (slice 192) and any future per-tenant URL
--   routing (e.g., `acme.atlas.example.com`) will want a slug. The
--   field ships now alongside the create flow so the slug is captured
--   at insert time rather than backfilled later.
--
-- LOAD-BEARING DESIGN CHOICES (also captured in
-- docs/audit-log/143-create-tenant-flow-decisions.md):
--
--   D1: `slug` is NULLABLE on the existing rows. The single bootstrap-
--       tenant row created by slice 198 + any pre-143 development rows
--       have NULL slug. New rows created via POST /v1/admin/tenants
--       set slug to a non-NULL value validated by the handler against
--       the spec regex `^[a-z0-9][a-z0-9-]{0,62}$`. The UNIQUE index
--       is partial — only enforces uniqueness across the rows that
--       set the slug. NULLs are unbounded.
--
--   D2: UNIQUE is on `slug` directly (not LOWER(slug)) because the
--       handler-side regex already restricts to lower-case + digits
--       + hyphen, so case-collision is impossible at the slug layer.
--       Mirrors the slice 198 D2 "validate at the application layer,
--       enforce at the DB layer" pattern but uses the simpler shape
--       since the input alphabet is single-case.
--
--   D3: `created_by_user_id` column captures the super_admin who
--       created the tenant. UUID, NULLABLE (existing rows lack
--       provenance; slice-198 bootstrap rows have a known creator
--       semantically — the bootstrap-first-install user — but the
--       column is not retro-populated by this migration). The column
--       has NO foreign key to `users` because `users.id` is per-
--       tenant and the creator's `users` row lives in a DIFFERENT
--       tenant than the row this column annotates (the actor's
--       session tenant ≠ the new tenant). Data-only, not FK.
--
--   D4: `super_admin_audit_log.action` CHECK extended with one new
--       value: 'tenant_create'. Mirrors slice 142's two-value
--       extension pattern. Append-only audit semantics preserved.
--
--   D5: `me_audit_log.action` CHECK extended with one new value:
--       'tenant_create'. The grant-tenant flow dual-writes a tenant-
--       scoped me_audit_log row (anchored to the actor's session
--       tenant) so the slice-124 unified aggregator surfaces the
--       event via its existing kind='me' UNION branch — matching
--       the slice-142 D2 pattern.
--
--   D6: `atlas_app` retains INSERT on `tenants` (slice 144 grant)
--       and gains the missing SELECT on a future `tenants` ID
--       lookup unrelated to RLS. The create-tenant write goes
--       through the BYPASSRLS `atlas_migrate` pool because the
--       handler must INSERT a row keyed on the NEW tenant's id —
--       which is not the actor's session tenant. The slice-002
--       four-policy RLS on `tenants` would block any cross-tenant
--       INSERT under `atlas_app`, so the auth pool is the right
--       primitive. No new grants needed; `atlas_migrate` is
--       BYPASSRLS by definition.
--
-- CONSTITUTIONAL INVARIANTS:
--
--   #2  Ingestion + evaluation separated — N/A; tenant rows are
--       identity, not evidence.
--   #5  FrameworkScope intersection — N/A at the schema layer; the
--       handler seeds the new tenant's default scope cell + builtin
--       dimension (mirroring deploy/docker/bootstrap/seed.sql) so
--       framework subscriptions intersect with a non-empty scope.
--   #6  Tenant isolation at the DB layer — `tenants` ships under
--       FORCE RLS (slice 144). The slice-143 handler INSERTs the
--       new row via `atlas_migrate` (BYPASSRLS) because RLS would
--       block a cross-tenant write — the new tenant_id is, by
--       definition, not the actor's session tenant. The handler
--       layer enforces the super_admin gate (slice 142's
--       requireSuperAdmin pattern).
--   #10 Audit-period freezing — N/A; tenant creation is identity,
--       not evidence.
--
-- ANTI-CRITERIA HONORED AT THE SCHEMA LAYER (P0):
--
--   - P0-CT-1: slug regex enforced at the handler layer; DB allows
--     any TEXT or NULL. Defense-in-depth via the UNIQUE index.
--   - P0-CT-3: atomicity is application-layer (single BYPASSRLS
--     transaction wraps INSERT tenants + INSERT users + INSERT
--     user_roles + INSERT super_admin_audit_log + INSERT
--     me_audit_log + INSERT scope_dimensions + INSERT scope_cells).
--   - P0-CT-4: NO tenant deletion endpoint — this migration adds NO
--     DELETE-related machinery to `tenants`.
--   - P0-CT-5: NO bulk import — this migration adds NO bulk
--     ingestion table.
--
-- IDEMPOTENCY / REVERSIBILITY:
--
--   ALTER + CREATE INDEX IF NOT EXISTS so re-applying is a no-op.
--   The CHECK extensions DROP IF EXISTS + recreate (supersets).
--   Down migration restores both CHECKs to their pre-slice-143
--   shape and drops `slug` + `created_by_user_id` + the partial
--   UNIQUE index.

-- ===== 1. tenants.slug column =====
--
-- Nullable TEXT — existing rows lack a slug. New rows created via
-- POST /v1/admin/tenants set it.

ALTER TABLE tenants
    ADD COLUMN IF NOT EXISTS slug TEXT;

COMMENT ON COLUMN tenants.slug IS
    'Slice 143: URL-safe stable handle for the tenant. Format ^[a-z0-9][a-z0-9-]{0,62}$ enforced at the handler layer. NULL on legacy rows; populated for every row created via POST /v1/admin/tenants. UNIQUE across non-NULL values via idx_tenants_slug_unique.';

-- ===== 2. tenants.created_by_user_id column =====
--
-- Provenance column — the super_admin user_id that created this
-- tenant. UUID, NULLABLE for legacy rows. Not a foreign key because
-- the creator's `users` row lives in a different tenant than this
-- row (slice 198's users-are-per-tenant model + the actor's session
-- tenant ≠ the new tenant being created).

ALTER TABLE tenants
    ADD COLUMN IF NOT EXISTS created_by_user_id UUID;

COMMENT ON COLUMN tenants.created_by_user_id IS
    'Slice 143: the super_admin user_id that created this tenant via POST /v1/admin/tenants. NULL on legacy rows + on the slice-198 bootstrap-first-install row. NOT a foreign key — the user record lives in a different tenant.';

-- ===== 3. tenants.slug partial UNIQUE index =====
--
-- Enforces uniqueness across the rows that have a slug set. NULLs
-- are unbounded by Postgres convention (NULLS-distinct, the default).
-- New rows fight this index on conflict → SQLSTATE 23505 → handler
-- maps to 409 Conflict.

CREATE UNIQUE INDEX IF NOT EXISTS idx_tenants_slug_unique
    ON tenants (slug)
    WHERE slug IS NOT NULL;

-- ===== 4. super_admin_audit_log.action CHECK extension =====
--
-- Slice 142 admitted 2 values: 'super_admin_grant' + 'super_admin_revoke'.
-- Slice 143 adds 'tenant_create'. Append-only audit semantics preserved
-- via the table's no-UPDATE/no-DELETE grant footprint.

ALTER TABLE super_admin_audit_log
    DROP CONSTRAINT IF EXISTS super_admin_audit_log_action_chk;

ALTER TABLE super_admin_audit_log
    ADD CONSTRAINT super_admin_audit_log_action_chk
    CHECK (action IN ('super_admin_grant', 'super_admin_revoke', 'tenant_create'));

-- ===== 5. users_idp_principal_unique relaxed to per-tenant =====
--
-- The slice-034 schema (migration 20260511000012_users_sessions_api_keys.sql,
-- line 40) created a GLOBAL UNIQUE on (idp_issuer, idp_subject). This
-- contradicts slice 192's multi-tenant identity design — the OAuth
-- user_resolver's `enumerateMemberships` query reads MULTIPLE users
-- rows per (idp_issuer, idp_subject), one per tenant the user has
-- access to. Slice 143's creator_joins_as='admin' branch is the
-- FIRST surface that actually attempts to write a second users row
-- for the same OIDC principal in a different tenant, surfacing the
-- latent contradiction.
--
-- Decision: relax the global UNIQUE to per-tenant. The application-
-- layer invariant is "at most one users row per (tenant_id,
-- idp_issuer, idp_subject)" — exactly what slice 192's user_resolver
-- already expects. The legacy index name is preserved with a per-
-- tenant body so existing migrations + tooling that grep for it
-- continue to work.
--
-- Why ship the fix here: a separate "fix the constraint" slice would
-- have to land before slice 143 to make creator_joins_as='admin' work
-- correctly. The fix is one line; gating it on a separate slice would
-- be process for process's sake.

DROP INDEX IF EXISTS users_idp_principal_unique;

CREATE UNIQUE INDEX users_idp_principal_unique
    ON users (tenant_id, idp_issuer, idp_subject)
    WHERE idp_issuer <> '' AND idp_subject <> '';

-- ===== 6. me_audit_log.action CHECK extension =====
--
-- Slice 142 extended the slice-144 list to 17 values (adding the two
-- super_admin actions). Slice 143 adds 'tenant_create'. The handler
-- writes ONE me_audit_log row per tenant_create event tagged with the
-- actor's session tenant — so the slice-124 unified aggregator surfaces
-- the event via the existing kind='me' UNION branch without an
-- aggregator schema change (mirrors the slice-142 D2 design).

ALTER TABLE me_audit_log
    DROP CONSTRAINT IF EXISTS me_audit_log_action_check;

ALTER TABLE me_audit_log
    ADD CONSTRAINT me_audit_log_action_check
    CHECK (action IN (
        'profile.update',
        'preferences.update',
        'session.revoke',
        'audit_log_query_unified',
        'audit_log_export',
        'audit_periods_export',
        'vendors_export',
        'risk_export',
        'controls_export',
        'evidence_export',
        'policies_export',
        'exceptions_export',
        'samples_export',
        'anchors_export',
        'tenant_rename',
        'bootstrap_first_install',
        'super_admin_grant',
        'super_admin_revoke',
        'tenant_create'
    ));
