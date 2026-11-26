-- security-atlas -- reverse 20261316000000_audit_period_frozen_no_truncate.sql.
--
-- Drops the BEFORE TRUNCATE guards and their function. Down is for
-- development only.

DROP TRIGGER IF EXISTS audit_period_frozen_no_truncate_trg ON audit_period_frozen_controls;
DROP TRIGGER IF EXISTS audit_period_frozen_no_truncate_trg ON audit_period_freeze_revisions;
DROP TRIGGER IF EXISTS audit_period_frozen_no_truncate_trg ON populations;
DROP TRIGGER IF EXISTS audit_period_frozen_no_truncate_trg ON audit_periods;
DROP FUNCTION IF EXISTS audit_period_frozen_no_truncate();
