-- security-atlas -- #2643: oidc_idp_group_mappings.idp_config_id gains a
-- tenant-composite FK to oidc_idp_configs.
--
-- 20260612030000_idp_group_role_mappings.sql left idp_config_id unchecked, so
-- a mapping could name another tenant's IdP config or one that does not
-- exist. The resolver filters by the signing-in tenant and its own config id,
-- so such a row never granted anything, but it was a dangling or cross-tenant
-- reference. This migration makes it (tenant_id, idp_config_id) ->
-- oidc_idp_configs(tenant_id, id) ON DELETE CASCADE: deleting a config
-- removes the mappings scoped to it. A NULL idp_config_id (the SCIM /
-- config-agnostic source) is not checked, by the default MATCH SIMPLE.
--
-- oidc_idp_configs exposes no UNIQUE (tenant_id, id); id alone is the PK, so
-- the pair is unique already and the constraint only gives the FK a target.
--
-- Pre-check: an existing dangling or cross-tenant mapping would make ADD
-- CONSTRAINT fail with a bare 23503. The DO block fails first, with the
-- counts, and never repairs or deletes rows: an operator decides what to do
-- with them before the constraint can land. The migration role is BYPASSRLS,
-- so the counts see every tenant.

DO $$
DECLARE
    n_missing      BIGINT;
    n_cross_tenant BIGINT;
BEGIN
    SELECT count(*) INTO n_missing
      FROM oidc_idp_group_mappings m
     WHERE m.idp_config_id IS NOT NULL
       AND NOT EXISTS (SELECT 1 FROM oidc_idp_configs c WHERE c.id = m.idp_config_id);

    SELECT count(*) INTO n_cross_tenant
      FROM oidc_idp_group_mappings m
      JOIN oidc_idp_configs c ON c.id = m.idp_config_id
     WHERE c.tenant_id <> m.tenant_id;

    IF n_missing > 0 OR n_cross_tenant > 0 THEN
        RAISE EXCEPTION 'group-role mappings with an unresolvable idp_config_id exist: nonexistent=%, cross_tenant=%; resolve them before applying this migration',
            n_missing, n_cross_tenant
            USING ERRCODE = 'check_violation';
    END IF;
END $$;

-- The FK depends on the UNIQUE, so a re-apply drops it first.
ALTER TABLE oidc_idp_group_mappings
    DROP CONSTRAINT IF EXISTS oidc_idp_group_mappings_idp_config_fk;

ALTER TABLE oidc_idp_configs
    DROP CONSTRAINT IF EXISTS oidc_idp_configs_tenant_id_id_unique,
    ADD CONSTRAINT oidc_idp_configs_tenant_id_id_unique UNIQUE (tenant_id, id);

ALTER TABLE oidc_idp_group_mappings
    ADD CONSTRAINT oidc_idp_group_mappings_idp_config_fk
        FOREIGN KEY (tenant_id, idp_config_id)
        REFERENCES oidc_idp_configs(tenant_id, id) ON DELETE CASCADE;
