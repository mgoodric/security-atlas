-- security-atlas — per-tenant feature flags + capability toggles (slice 059).
--
-- Two tables in one migration:
--   feature_flags             - mutable per-tenant capability toggles. Composite
--                               PK (tenant_id, flag_key); the application is the
--                               single writer (upsert on toggle). NULL last_changed_*
--                               on a never-toggled row distinguishes "shipped
--                               default" from "operator decision recorded".
--   feature_flag_audit_log    - append-only state-transition log. Every toggle
--                               writes one row; the table has SELECT + INSERT
--                               policies ONLY under FORCE RLS, which makes it
--                               append-only by construction (same pattern as
--                               exception_audit_log, evidence_audit_log,
--                               sample_audit_log, decision_audit_log).
--
-- Constitutional invariants honored:
--   #6  Tenant isolation at the database layer. Both tables get FORCE ROW
--       LEVEL SECURITY. `feature_flags` uses the four-policy split (tenant_read
--       FOR SELECT, tenant_write FOR INSERT WITH CHECK, tenant_update FOR
--       UPDATE USING + WITH CHECK, tenant_delete FOR DELETE) established by
--       slices 011 / 014 / 017 / 018 / 021 / 035 / 036.
--       `feature_flag_audit_log` uses SELECT + INSERT policies only — the
--       explicit absence of UPDATE/DELETE policies under FORCE makes the
--       table append-only.
--
-- Anti-criteria honored at the schema layer (P0):
--   - Spine flags do NOT exist at the schema level: there is no enum entry
--     for spine-only namespaces. The seed list in internal/featureflag/seed.go
--     enumerates the 12 capability flags; a unit test asserts none of them
--     fall under a spine-forbidden prefix (rls., tenancy., auth., scope.,
--     evidence.ledger., framework.crosswalk., schema.registry.). The DB does
--     not police flag_key contents beyond a non-empty CHECK because the
--     spine-forbidden list is an application policy, not a schema invariant
--     (a future slice may add new capability namespaces without a migration).
--   - flag_key is a non-empty TEXT identifier (snake_case namespaced
--     convention enforced at the application layer; the schema accepts
--     anything non-empty so adding new flag namespaces does not require a
--     migration).
--
-- Migration is reversible via 20260511000019_feature_flags.down.sql which
-- drops both tables in dependency order. No row seeding here — the Go Store
-- returns the seed default when a row is absent (the operator's first toggle
-- is the first INSERT for that flag_key).

-- ===== 1. feature_flags =====

CREATE TABLE feature_flags (
    tenant_id           UUID NOT NULL,
    flag_key            TEXT NOT NULL,
    enabled             BOOLEAN NOT NULL,
    description         TEXT NOT NULL DEFAULT '',
    -- category bounds the UI grouping. The 9 enum entries cover the v1
    -- capability inventory (canvas §10.1); future capabilities may
    -- introduce new categories via a CHECK constraint relaxation in a
    -- future migration. core/risk/vendor/policy/controls/audit/evidence/
    -- board/integrations match the issue's seed table.
    category            TEXT NOT NULL,
    last_changed_by     TEXT NULL,
    last_changed_at     TIMESTAMPTZ NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (tenant_id, flag_key),

    CONSTRAINT feature_flags_flag_key_nonempty
        CHECK (length(flag_key) > 0),
    CONSTRAINT feature_flags_category_chk
        CHECK (category IN (
            'core', 'risk', 'vendor', 'policy', 'controls',
            'audit', 'evidence', 'board', 'integrations'
        ))
);

-- Tenant-scoped list query hits the PK directly (tenant_id is the leading
-- column of the composite PK). No additional index needed.

-- ===== 2. feature_flag_audit_log =====
--
-- Append-only state-transition log. Every toggle writes one row including
-- the system-driven first-write (no prior row) — captured by from_enabled
-- = the seed default. No FK to feature_flags because the audit trail must
-- survive a hypothetical future admin-cleanup; flag_key is preserved
-- verbatim.

CREATE TABLE feature_flag_audit_log (
    id              UUID PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    flag_key        TEXT NOT NULL,
    from_enabled    BOOLEAN NOT NULL,
    to_enabled      BOOLEAN NOT NULL,
    actor           TEXT NOT NULL,
    reason          TEXT NOT NULL DEFAULT '',
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT feature_flag_audit_log_actor_nonempty
        CHECK (length(actor) > 0),
    CONSTRAINT feature_flag_audit_log_flag_key_nonempty
        CHECK (length(flag_key) > 0)
);

CREATE INDEX idx_feature_flag_audit_log_tenant_occurred
    ON feature_flag_audit_log (tenant_id, occurred_at DESC);

CREATE INDEX idx_feature_flag_audit_log_tenant_flag_occurred
    ON feature_flag_audit_log (tenant_id, flag_key, occurred_at DESC);

-- ===== Row-Level Security =====

ALTER TABLE feature_flags ENABLE ROW LEVEL SECURITY;
ALTER TABLE feature_flags FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON feature_flags
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON feature_flags
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON feature_flags
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON feature_flags
    FOR DELETE USING (current_tenant_matches(tenant_id));

-- feature_flag_audit_log is append-only by construction: SELECT + INSERT
-- policies only. No UPDATE/DELETE policy under FORCE ROW LEVEL SECURITY
-- means atlas_app cannot mutate audit rows. Mirrors slice 011's
-- exception_audit_log, slice 013's evidence_audit_log, slice 026's
-- sample_audit_log, slice 035's decision_audit_log, slice 036's
-- artifact_access_log.
ALTER TABLE feature_flag_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE feature_flag_audit_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON feature_flag_audit_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON feature_flag_audit_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON feature_flags TO atlas_app;
GRANT SELECT, INSERT ON feature_flag_audit_log TO atlas_app;
