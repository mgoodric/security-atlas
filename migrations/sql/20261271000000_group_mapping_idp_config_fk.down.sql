-- Reverse #2643: drop the tenant-composite idp_config FK on
-- oidc_idp_group_mappings, then the (tenant_id, id) UNIQUE it targets.

ALTER TABLE oidc_idp_group_mappings
    DROP CONSTRAINT IF EXISTS oidc_idp_group_mappings_idp_config_fk;

ALTER TABLE oidc_idp_configs
    DROP CONSTRAINT IF EXISTS oidc_idp_configs_tenant_id_id_unique;
