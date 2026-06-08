-- Down migration for 20260608010000_oscal_component_definitions.sql
-- (slice 512). Drops the two new tables + restores the slice-511 CHECK
-- shapes (catalog | profile only).

DROP TABLE IF EXISTS imported_component_claims;
DROP TABLE IF EXISTS imported_components;

DROP INDEX IF EXISTS idx_imported_catalogs_tenant_components;

-- Restore the slice-511 audit action CHECK (catalog + profile actions).
ALTER TABLE imported_catalog_audit_log
    DROP CONSTRAINT imported_catalog_audit_log_action_chk;
ALTER TABLE imported_catalog_audit_log
    ADD CONSTRAINT imported_catalog_audit_log_action_chk
        CHECK (action IN (
            'catalog_imported',
            'import_rejected',
            'profile_imported',
            'profile_import_rejected'
        ));

-- Restore the slice-511 source CHECK (catalog + profile import).
ALTER TABLE imported_catalogs
    DROP CONSTRAINT imported_catalogs_source_chk;
ALTER TABLE imported_catalogs
    ADD CONSTRAINT imported_catalogs_source_chk
        CHECK (source IN ('oscal-import', 'oscal-profile-import'));

-- Restore the slice-511 kind CHECK (catalog | profile).
ALTER TABLE imported_catalogs
    DROP CONSTRAINT imported_catalogs_kind_chk;
ALTER TABLE imported_catalogs
    ADD CONSTRAINT imported_catalogs_kind_chk
        CHECK (kind IN ('catalog', 'profile'));
