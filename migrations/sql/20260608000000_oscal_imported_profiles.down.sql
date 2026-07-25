-- Down migration for 20260608000000_oscal_imported_profiles.sql (slice 511).
-- Restores the slice-492 CHECK shapes and drops the two added columns.

DROP INDEX IF EXISTS idx_imported_catalogs_tenant_profiles;

-- Restore the slice-492 audit action CHECK (catalog actions only).
ALTER TABLE imported_catalog_audit_log
    DROP CONSTRAINT imported_catalog_audit_log_action_chk;
ALTER TABLE imported_catalog_audit_log
    ADD CONSTRAINT imported_catalog_audit_log_action_chk
        CHECK (action IN ('catalog_imported', 'import_rejected'));

-- Restore the slice-492 source CHECK (catalog import only).
ALTER TABLE imported_catalogs
    DROP CONSTRAINT imported_catalogs_source_chk;
ALTER TABLE imported_catalogs
    ADD CONSTRAINT imported_catalogs_source_chk
        CHECK (source IN ('oscal-import'));

ALTER TABLE imported_catalogs
    DROP CONSTRAINT imported_catalogs_kind_chk;
ALTER TABLE imported_catalogs
    DROP COLUMN profile_title,
    DROP COLUMN kind;
