-- security-atlas -- reverse 20261307000000_audit_period_freeze_immutable.sql.
--
-- Drops both guards. Rows are untouched; the original freeze goes back to
-- being immutable in application code only.

DROP TRIGGER IF EXISTS audit_period_freeze_revisions_chain_trg ON audit_period_freeze_revisions;
DROP FUNCTION IF EXISTS audit_period_freeze_revisions_chain();

DROP TRIGGER IF EXISTS audit_periods_freeze_immutable_trg ON audit_periods;
DROP FUNCTION IF EXISTS audit_periods_freeze_immutable();
