-- security-atlas — slice 144: rename-tenant flow.
--
-- Adds the `tenants` table (canonical tenant identity row), wires
-- it under FORCE ROW LEVEL SECURITY via the slice-002 four-policy
-- pattern, and extends `me_audit_log.action` CHECK to permit the
-- new `'tenant_rename'` value.
--
-- BACKGROUND — why a `tenants` table now:
--
--   Through v1, tenants were referenced purely by UUID. The
--   `tenant_id` column on every primary table was a bare UUID with
--   no parent row; a tenant "existed" by being referenced (see the
--   seed.sql comment on docker-compose bootstrap). Slice 192 (the
--   auth-substrate-v2 spine completion) introduced a `GET
--   /v1/me/tenants` handler that issues `SELECT id, name FROM tenants
--   WHERE id = ANY($1)` against the BYPASSRLS pool — assuming this
--   table exists. The handler ships a graceful-fallback path (empty
--   name on missing row), so the substrate works today; but the
--   moment an operator renames a tenant via the new
--   `PATCH /v1/tenants/{id}` (this slice's surface), the table MUST
--   exist as a real persistence target.
--
-- Decisions (see docs/audit-log/144-rename-tenant-decisions.md for
-- the full grilled-out trail):
--
--   D1: case-insensitive uniqueness on `name`. Implemented via a
--       partial-or-full UNIQUE expression index on `LOWER(name)`.
--       Slice 192's tenant-switcher dropdown is keyed by name; trivial
--       impersonation via `Acme` vs `acme` would collide on the picker
--       UI. Blocking at the DB layer is defense-in-depth on top of the
--       handler's normalization (P0-RT-2 anti-criterion).
--   D2: NFC normalization happens at the application layer (Go), NOT
--       in the DB. Postgres `unicode_normalize` is available but
--       requires libicu; the application normalizes via golang.org/x/text/unicode/norm.
--       The DB stores the normalized form; future operators reading
--       directly via psql see canonical text.
--   D3: 64-byte UTF-8 cap is also application-side (Go enforces; DB
--       accepts TEXT). Mirrors slice-024 vendor.name and slice-022
--       policies.name patterns (also application-capped).
--   D4: `is_bootstrap_tenant` carried forward from slice 141's
--       intended schema. Partial unique index on the `WHERE
--       is_bootstrap_tenant = true` predicate serializes first-install
--       races (slice 141 P0-ELEVATE-2). v1 ships the column inert —
--       the slice-141 OIDC bootstrap branch landed via the OAuth
--       substrate (slice 192) and does not write to this table; the
--       column is here so the slice that does (future slice 198
--       OIDC-first-install bootstrap) can switch it on without a
--       migration round-trip.
--   D5: Unicode confusables detection is NOT enforced at the DB
--       layer. Slice 144 spec AC-6 records the accepted-risk
--       decision: a fully-formed confusables guard would either
--       require a Postgres extension (`pg_trgm` + custom function)
--       or a heavy Go library; v1 documents the residual risk and
--       relies on the UI's tenant-switcher rendering canonical
--       characters via `font-feature-settings: "ss01"` (slice 192
--       D5). Future hardening lands when a maintainer trips the
--       residual case in the wild.
--
-- Constitutional invariants honored:
--
--   #2 Ingestion + evaluation separated — N/A; tenants are identity
--      rows, not evidence.
--   #6 Tenant isolation enforced at the DB layer — the new table
--      ships under FORCE RLS with the slice-002 four-policy pattern
--      (`tenant_read` / `tenant_write_insert` / `tenant_write_update`
--      / `tenant_write_delete`). Tenant A admin cannot read or rename
--      Tenant B's row (P0-RT-3, also enforced upstream via OPA).
--   #10 Audit-period freezing — N/A; this table holds identity rows,
--      not evidence.
--
-- Anti-criteria honored at the schema layer:
--
--   - P0-RT-2: case-insensitive uniqueness — `idx_tenants_lower_name`
--     UNIQUE expression index. Inserts that vary only by case raise
--     `unique_violation`; the handler maps to 409.
--   - P0-RT-3: audit-log row in same transaction — the handler writes
--     `me_audit_log` row in the same tx as `UPDATE tenants`. CHECK
--     extension below admits the new action value.
--   - P0-RT-4: NO branding/logo/metadata fields — schema ships
--     exactly `id`, `name`, `is_bootstrap_tenant`, `created_at`,
--     `updated_at`. Future fields are filed as separate slices.
--
-- Idempotency / reversibility:
--
--   CREATE TABLE IF NOT EXISTS so re-applying is a no-op. RLS
--   policies are wrapped in DO blocks so the migration is safe on
--   a partially-applied database. The companion down.sql drops the
--   table + restores the prior me_audit_log CHECK.

-- ===== 1. tenants table =====

CREATE TABLE IF NOT EXISTS tenants (
    id                    UUID NOT NULL PRIMARY KEY,
    name                  TEXT NOT NULL,
    is_bootstrap_tenant   BOOLEAN NOT NULL DEFAULT false,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Defense-in-depth: NULL or empty name is a catastrophic UX bug
    -- (the slice-192 picker shows the empty-string row as a blank
    -- entry). NOT NULL above is the first leg; this CHECK is the
    -- second.
    CONSTRAINT tenants_name_nonempty CHECK (length(btrim(name)) > 0)
);

COMMENT ON TABLE tenants IS
    'Slice 144: canonical tenant identity row. Adopted late — through v1, tenant_id was a bare UUID with no parent. The slice-192 `GET /v1/me/tenants` handler reads `name` from here; `PATCH /v1/tenants/{id}` (slice 144) mutates it under per-tenant admin or super_admin authority.';

COMMENT ON COLUMN tenants.name IS
    'Slice 144: human-readable label rendered in the slice-192 tenant-switcher. Application-side normalized to NFC + trimmed + capped at 64 UTF-8 bytes (Go layer). Case-insensitive UNIQUE via `idx_tenants_lower_name`.';

COMMENT ON COLUMN tenants.is_bootstrap_tenant IS
    'Slice 144: carried forward from slice 141 intent. Future slice 198 (OIDC-first-install bootstrap) flips this on the install-time tenant row to serialize concurrent first-installer OIDC callbacks via `idx_tenants_bootstrap_singleton`.';

-- ===== 2. Case-insensitive uniqueness on name =====
--
-- Expression index on LOWER(name) — Postgres makes this both the
-- uniqueness constraint AND the lookup-by-name index in one shot.
-- The application-side normalization ensures the LOWER() output is
-- stable across NFC variants (Go's `norm.NFC` runs first; LOWER()
-- then collapses Cyrillic + Latin case variations within their
-- script).

CREATE UNIQUE INDEX IF NOT EXISTS idx_tenants_lower_name
    ON tenants (LOWER(name));

-- ===== 3. Bootstrap singleton =====
--
-- Partial unique index — only at most one row may have
-- `is_bootstrap_tenant = true`. Slice 141 P0-ELEVATE-2 serializes
-- concurrent first-install bootstrap attempts: the second writer
-- loses to `unique_violation` and falls through to the
-- established-install branch.

CREATE UNIQUE INDEX IF NOT EXISTS idx_tenants_bootstrap_singleton
    ON tenants (is_bootstrap_tenant)
    WHERE is_bootstrap_tenant = true;

-- ===== 4. updated_at touch trigger =====
--
-- Keep updated_at in sync without forcing every UPDATE site to set
-- it explicitly. Matches the slice-024 vendor / slice-022 policy
-- table pattern.

CREATE OR REPLACE FUNCTION tenants_touch_updated_at()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_tenants_touch_updated_at ON tenants;
CREATE TRIGGER trg_tenants_touch_updated_at
    BEFORE UPDATE ON tenants
    FOR EACH ROW
    EXECUTE FUNCTION tenants_touch_updated_at();

-- ===== 5. RLS — four-policy pattern =====
--
-- The `tenants` table contains one row per tenant; the row's PK
-- `id` IS the tenant_id (no separate column). The standard slice-002
-- `current_tenant_matches(row_tenant)` helper takes the row's
-- tenant id, so the policy USING clauses pass `id` directly.

ALTER TABLE tenants ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenants FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_read ON tenants;
CREATE POLICY tenant_read ON tenants
    FOR SELECT
    USING (current_tenant_matches(id));

DROP POLICY IF EXISTS tenant_write_insert ON tenants;
CREATE POLICY tenant_write_insert ON tenants
    FOR INSERT
    WITH CHECK (current_tenant_matches(id));

DROP POLICY IF EXISTS tenant_write_update ON tenants;
CREATE POLICY tenant_write_update ON tenants
    FOR UPDATE
    USING (current_tenant_matches(id))
    WITH CHECK (current_tenant_matches(id));

DROP POLICY IF EXISTS tenant_write_delete ON tenants;
CREATE POLICY tenant_write_delete ON tenants
    FOR DELETE
    USING (current_tenant_matches(id));

-- ===== 6. Role grants =====
--
-- atlas_app: SELECT / INSERT / UPDATE. No DELETE in v1 — tenant
-- removal is a separate slice with retention-policy semantics. The
-- slice-141 OIDC bootstrap (future slice 198) INSERTs the first
-- row via the application user under the bootstrap branch.
GRANT SELECT, INSERT, UPDATE ON tenants TO atlas_app;

-- atlas_service_account: SELECT only. The slice-192 `GET
-- /v1/me/tenants` handler uses the authPool (atlas_migrate or
-- atlas_service_account depending on env) to enrich tenant names
-- before the caller's tenant context is even established. The
-- SELECT-only grant matches the read-only nature of that path.
GRANT SELECT ON tenants TO atlas_service_account;

-- ===== 7. me_audit_log.action CHECK extension =====
--
-- New `'tenant_rename'` value joins the existing eleven allowed
-- actions. Slice 144 writes a `me_audit_log` row per successful
-- rename in the same transaction as the UPDATE.

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
        'tenant_rename'
    ));
