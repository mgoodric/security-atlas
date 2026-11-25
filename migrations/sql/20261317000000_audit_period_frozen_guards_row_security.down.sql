-- security-atlas -- reverse 20261317000000_audit_period_frozen_guards_row_security.sql.
--
-- Drops the row_security setting from both guards, restoring the
-- definitions 20261314000000 and 20261316000000 left. Down is for
-- development only.

ALTER FUNCTION audit_period_frozen_no_truncate() RESET row_security;
ALTER FUNCTION audit_periods_frozen_has_controls() RESET row_security;
