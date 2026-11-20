-- security-atlas -- #2760: a group-derived role records which source
-- derived it.
--
-- user_roles has one row per (tenant, user, role), so a role that two
-- sources both derive (two OIDC configs, or an OIDC config and SCIM) is one
-- row with origin='group-derived' and no record of who granted it. The
-- resolver diffed every source's target set against that one unscoped set,
-- so a reconcile through source B revoked a role only source A had
-- derived, and a deleted config's roles stayed with any user who had no
-- other source to reconcile through.
--
-- user_role_group_sources holds one row per (tenant, user, role, source).
-- idp_config_id is the oidc_idp_configs row the derivation came from, or
-- NULL for SCIM (the same convention as oidc_idp_group_mappings). The
-- resolver reads and diffs only its own source's rows, and removes the
-- user_roles group-derived row only when no source row for it remains.
--
-- Two foreign keys, both ON DELETE CASCADE:
--   (tenant_id, user_id, role) -> user_roles: a role removed any other way
--     (PATCH /v1/admin/users/{id}/roles replaces the whole set) takes its
--     source rows with it, so no source row outlives its role.
--   (tenant_id, idp_config_id) -> oidc_idp_configs: DELETE /v1/admin/sso
--     revokes a config's roles before it deletes the config; the cascade
--     covers a config an operator deletes by SQL. A NULL idp_config_id
--     (SCIM) is not checked, by the default MATCH SIMPLE.
--
-- Backfill. Every existing group-derived role is attributed to each source
-- whose latest group_role_audit_log entry for that (tenant, user, role) is
-- a grant. The resolver has audited every grant and revoke since slice
-- 509, so that is the derivation state the log records. A role with no
-- such entry (written by hand) gets no source row: no reconcile revokes
-- it, and an admin removes it through the users API. A role whose source
-- is a config that no longer exists gets none either.
--
-- Reversible via 20261272000000_user_role_group_sources.down.sql.

CREATE TABLE user_role_group_sources (
    tenant_id     UUID NOT NULL,
    user_id       TEXT NOT NULL,
    role          TEXT NOT NULL,
    -- NULL = SCIM; non-NULL = the OIDC config whose mappings derived it.
    idp_config_id UUID NULL,
    granted_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT user_role_group_sources_user_role_fk
        FOREIGN KEY (tenant_id, user_id, role)
        REFERENCES user_roles(tenant_id, user_id, role) ON DELETE CASCADE,
    CONSTRAINT user_role_group_sources_idp_config_fk
        FOREIGN KEY (tenant_id, idp_config_id)
        REFERENCES oidc_idp_configs(tenant_id, id) ON DELETE CASCADE
);

-- One row per source. COALESCE so the SCIM source (NULL) is de-duplicated
-- too, the oidc_idp_group_mappings_unique convention.
CREATE UNIQUE INDEX user_role_group_sources_unique
    ON user_role_group_sources (
        tenant_id,
        user_id,
        role,
        COALESCE(idp_config_id, '00000000-0000-0000-0000-000000000000'::uuid)
    );

-- The config delete lists a config's rows.
CREATE INDEX user_role_group_sources_config
    ON user_role_group_sources (tenant_id, idp_config_id);

ALTER TABLE user_role_group_sources ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_role_group_sources FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON user_role_group_sources
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON user_role_group_sources
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON user_role_group_sources
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON user_role_group_sources
    FOR DELETE USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON user_role_group_sources TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON user_role_group_sources TO atlas_migrate;

-- Backfill (the migration role is BYPASSRLS, so this sees every tenant).
INSERT INTO user_role_group_sources (tenant_id, user_id, role, idp_config_id)
SELECT ur.tenant_id, ur.user_id, ur.role, latest.idp_config_id
  FROM user_roles ur
  JOIN LATERAL (
        SELECT DISTINCT ON (COALESCE(a.idp_config_id, '00000000-0000-0000-0000-000000000000'::uuid))
               a.idp_config_id, a.change
          FROM group_role_audit_log a
         WHERE a.tenant_id = ur.tenant_id
           AND a.user_id = ur.user_id
           AND a.role = ur.role
         ORDER BY COALESCE(a.idp_config_id, '00000000-0000-0000-0000-000000000000'::uuid),
                  a.occurred_at DESC, a.id
       ) latest ON latest.change = 'grant'
 WHERE ur.origin = 'group-derived'
   AND (latest.idp_config_id IS NULL
        OR EXISTS (SELECT 1 FROM oidc_idp_configs c
                    WHERE c.tenant_id = ur.tenant_id AND c.id = latest.idp_config_id))
ON CONFLICT DO NOTHING;
