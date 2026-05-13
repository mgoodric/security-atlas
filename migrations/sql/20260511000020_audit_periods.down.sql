-- security-atlas — reverse the AuditPeriod + freezing primitive migration
-- (slice 028). Drops both tables and the populations.audit_period_id column
-- in dependency order.

-- Drop the FK + column on populations first; the FK target is audit_periods.
DROP INDEX IF EXISTS idx_populations_tenant_audit_period;
ALTER TABLE populations
    DROP CONSTRAINT IF EXISTS populations_audit_period_fk;
ALTER TABLE populations
    DROP COLUMN IF EXISTS audit_period_id;

-- Append-only log first (no inbound FKs).
DROP INDEX IF EXISTS idx_audit_period_audit_log_tenant_occurred;
DROP INDEX IF EXISTS idx_audit_period_audit_log_tenant_period;
DROP TABLE IF EXISTS audit_period_audit_log;

-- Then the audit_periods table.
DROP INDEX IF EXISTS idx_audit_periods_tenant_framework;
DROP INDEX IF EXISTS idx_audit_periods_tenant_status;
DROP TABLE IF EXISTS audit_periods;
