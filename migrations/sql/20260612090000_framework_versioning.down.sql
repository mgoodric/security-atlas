-- Reverses 20260612090000_framework_versioning.sql (slice 484).
--
-- Drops the capability-layer objects + revokes the narrow lifecycle grants.
-- Touches NO existing data and removes NO pre-existing enum value (the slice
-- added the framework_version_status `superseded` semantic by REUSING the
-- existing `legacy` value, so there is nothing to remove from that enum). The
-- down is therefore clean and additive-reversible (P0-484).

-- Trigger + function first (they depend on framework_requirements only).
DROP TRIGGER IF EXISTS trg_framework_requirement_immutability ON framework_requirements;
DROP FUNCTION IF EXISTS enforce_framework_requirement_immutability();

-- Narrow lifecycle grants.
REVOKE UPDATE (status) ON framework_versions FROM atlas_app;
REVOKE UPDATE (latest_version_id) ON frameworks FROM atlas_app;

-- Audit table references framework_version_migrations (migration_id FK), so
-- drop the audit table first, then the queue.
DROP TABLE IF EXISTS framework_version_audit;
DROP TABLE IF EXISTS framework_version_migrations;

-- Enums last (no remaining dependents once the tables are gone).
DROP TYPE IF EXISTS framework_version_audit_action;
DROP TYPE IF EXISTS framework_version_migration_match_kind;
DROP TYPE IF EXISTS framework_version_migration_status;
