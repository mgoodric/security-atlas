-- security-atlas — reversal of slice 059 (per-tenant feature flags).
--
-- Drops both tables in dependency order. feature_flag_audit_log has no FK
-- to feature_flags (deliberate -- audit survives flag-row cleanup), so
-- the drop order is arbitrary; we drop the audit log first to mirror the
-- forward migration's stack order.

DROP TABLE IF EXISTS feature_flag_audit_log;
DROP TABLE IF EXISTS feature_flags;
