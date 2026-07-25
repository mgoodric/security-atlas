-- security-atlas — slice 509: IdP group-to-role mapping.
--
-- The authorization-derivation sibling of slice 508 (SCIM user lifecycle):
-- 508 provisions the user, 509 maps their IdP groups to atlas roles. Because
-- this surface ASSIGNS ROLES (the thing 508 deliberately deferred, P0-508-3),
-- the security model is the load-bearing part. See docs/issues/509-*.md STRIDE.
--
-- Owns TWO new tenant-scoped tables + an additive marker on user_roles:
--
--   - oidc_idp_group_mappings : per-tenant admin-managed mapping from an IdP
--                               group (by id OR name, per the IdP — matched
--                               literally as text) to one or more EXISTING
--                               atlas roles. Keyed by (tenant, idp_config_id,
--                               group_ref, role) so a tenant with multiple IdP
--                               configs maps each IdP's groups independently
--                               (AC-6). idp_config_id is NULLABLE: a NULL
--                               source means "SCIM / IdP-config-agnostic" (the
--                               SCIM push channel is the tenant's single
--                               credential, not an oidc_idp_configs row); a
--                               non-NULL value references the specific OIDC
--                               relying-party config the groups claim came from.
--
--   - group_role_audit_log    : append-only ledger of every group-DERIVED role
--                               grant/revoke (AC-7 / STRIDE-R). Mirrors the
--                               slice-035 decision_audit_log + slice-508
--                               scim_audit_log append-only pattern: SELECT +
--                               INSERT policies only, no UPDATE/DELETE, FORCE
--                               RLS. Captures the triggering group + source
--                               (oidc vs scim) so "why does this user have this
--                               role?" is always answerable.
--
--   - user_roles.origin       : 'manual' | 'group-derived'. Distinguishes a
--                               manual admin assignment from a group-derived
--                               one so manual roles SURVIVE a group
--                               re-derivation (AC-4): re-derivation only
--                               DELETEs/INSERTs origin='group-derived' rows and
--                               never touches origin='manual' rows. Defaults
--                               'manual' so every existing row (slice-035/478
--                               INSERT paths + older fixtures) stays valid
--                               without a backfill.
--
-- Anti-criteria honored at the schema layer (P0-509-*):
--   - P0-509-1 fail-closed: an unmapped group has NO row here, so the resolver
--     derives no role — the mapping table IS the allow-list.
--   - P0-509-4 no auto-create roles: the role column carries the SAME 5-role
--     CHECK as user_roles. A mapping can only reference one of the canonical
--     atlas roles; a mapping naming a non-existent role is rejected at write
--     time by the CHECK (and the application validates against authz.IsCanonical
--     before the INSERT for a clean 400).
--
-- All reads/writes run under app.current_tenant RLS (invariant #6): a mapping
-- for tenant A can never read or mutate tenant B. Four-policy FORCE RLS using
-- the slice-002 current_tenant_matches helper.
--
-- Migration slot is `20260612030000` (after slice 508's `20260612020000`).
-- Plain SQL (Atlas community caveat — no HCL row_security blocks).
--
-- Issue: docs/issues/509-idp-group-to-role-mapping.md
-- Reversible via 20260612030000_idp_group_role_mappings.down.sql.

-- ===== user_roles.origin (additive) =====

ALTER TABLE user_roles
    ADD COLUMN IF NOT EXISTS origin TEXT NOT NULL DEFAULT 'manual';

ALTER TABLE user_roles
    ADD CONSTRAINT user_roles_origin_chk
        CHECK (origin IN ('manual', 'group-derived'));

-- ===== oidc_idp_group_mappings =====

CREATE TABLE oidc_idp_group_mappings (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL,
    -- idp_config_id NULL = SCIM / IdP-config-agnostic source; non-NULL = the
    -- specific oidc_idp_configs row the OIDC groups claim is scoped to (AC-6).
    idp_config_id UUID NULL,
    -- group_ref is the IdP's group identifier — an id OR a name, per the IdP
    -- (matched literally; the resolver compares the validated claim/SCIM group
    -- value to this text verbatim).
    group_ref     TEXT NOT NULL,
    role          TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by    UUID NULL,

    -- Same 5-role enum as user_roles (P0-509-4): a mapping may only target an
    -- EXISTING canonical atlas role; never auto-creates a role from a group
    -- name.
    CONSTRAINT oidc_idp_group_mappings_role_chk
        CHECK (role IN ('admin', 'grc_engineer', 'control_owner', 'auditor', 'viewer')),
    CONSTRAINT oidc_idp_group_mappings_group_ref_nonempty
        CHECK (length(group_ref) > 0)
);

-- Uniqueness over (tenant, source, group, role). PostgreSQL treats NULLs as
-- distinct in a plain UNIQUE constraint, which would let the SCIM source
-- (idp_config_id IS NULL) insert duplicate (group, role) rows. COALESCE the
-- NULL to the nil UUID so the SCIM source is also de-duplicated — the mapping
-- is idempotent for both source kinds (AC-1).
CREATE UNIQUE INDEX oidc_idp_group_mappings_unique
    ON oidc_idp_group_mappings (
        tenant_id,
        COALESCE(idp_config_id, '00000000-0000-0000-0000-000000000000'::uuid),
        group_ref,
        role
    );

-- Hot path: the resolver looks up all roles for a (tenant, source, group-set).
CREATE INDEX oidc_idp_group_mappings_lookup
    ON oidc_idp_group_mappings (tenant_id, idp_config_id, group_ref);

ALTER TABLE oidc_idp_group_mappings ENABLE ROW LEVEL SECURITY;
ALTER TABLE oidc_idp_group_mappings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON oidc_idp_group_mappings
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON oidc_idp_group_mappings
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON oidc_idp_group_mappings
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON oidc_idp_group_mappings
    FOR DELETE USING (current_tenant_matches(tenant_id));

-- ===== group_role_audit_log =====
--
-- Append-only ledger of every group-DERIVED role change (AC-7). SELECT + INSERT
-- policies only — no UPDATE/DELETE — so rows are immutable once written, even by
-- atlas_app, exactly like decision_audit_log (slice 035) + scim_audit_log
-- (slice 508).
--
-- source enumerates the derivation source ('oidc' | 'scim'). change enumerates
-- the action ('grant' | 'revoke'). triggering_group records WHICH group caused
-- the change (STRIDE-R answerability). user_id is the affected atlas user.
CREATE TABLE group_role_audit_log (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID NOT NULL,
    occurred_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id          TEXT NOT NULL,
    role             TEXT NOT NULL,
    change           TEXT NOT NULL,
    source           TEXT NOT NULL,
    idp_config_id    UUID NULL,
    triggering_group TEXT NOT NULL DEFAULT '',
    detail           JSONB NOT NULL DEFAULT '{}'::jsonb,

    CONSTRAINT group_role_audit_log_change_chk
        CHECK (change IN ('grant', 'revoke')),
    CONSTRAINT group_role_audit_log_source_chk
        CHECK (source IN ('oidc', 'scim')),
    CONSTRAINT group_role_audit_log_user_id_nonempty
        CHECK (length(user_id) > 0)
);

CREATE INDEX group_role_audit_log_tenant_occurred
    ON group_role_audit_log (tenant_id, occurred_at DESC);
CREATE INDEX group_role_audit_log_tenant_user
    ON group_role_audit_log (tenant_id, user_id, occurred_at DESC);

ALTER TABLE group_role_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE group_role_audit_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON group_role_audit_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON group_role_audit_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
-- Intentionally NO update/delete policies — append-only.

-- ===== grants =====

GRANT SELECT, INSERT, UPDATE, DELETE ON oidc_idp_group_mappings TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON oidc_idp_group_mappings TO atlas_migrate;

-- group_role_audit_log: append-only — no UPDATE/DELETE grant to atlas_app.
GRANT SELECT, INSERT ON group_role_audit_log TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON group_role_audit_log TO atlas_migrate;
