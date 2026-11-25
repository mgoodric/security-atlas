-- security-atlas -- reverse 20261315000000_audit_period_frozen_has_controls_revoke.sql.
--
-- Restores the default PUBLIC EXECUTE on audit_periods_frozen_has_controls().
-- Down is for development only.

GRANT EXECUTE ON FUNCTION audit_periods_frozen_has_controls() TO PUBLIC;
