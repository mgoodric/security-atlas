-- security-atlas — reverse the auditor assignments + audit notes migration
-- (slice 025). Drops both tables in reverse dependency order.

DROP INDEX IF EXISTS idx_audit_notes_tenant_period;
DROP INDEX IF EXISTS idx_audit_notes_tenant_author_period;
DROP TABLE IF EXISTS audit_notes;

DROP TABLE IF EXISTS auditor_assignments;
