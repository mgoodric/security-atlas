-- security-atlas -- reverse 20261313000000_audit_period_frozen_no_delete.sql.
--
-- Drops the guard. Rows are untouched; a frozen period is deletable again by
-- any role holding DELETE on audit_periods.

DROP TRIGGER IF EXISTS audit_periods_frozen_no_delete_trg ON audit_periods;
DROP FUNCTION IF EXISTS audit_periods_frozen_no_delete();
