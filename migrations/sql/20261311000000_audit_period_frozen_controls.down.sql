-- security-atlas -- reverse 20261311000000_audit_period_frozen_controls.sql.
--
-- Drops the recorded control sets. Replay goes back to reading the live
-- catalog, so a control created or deleted after a freeze breaks replay
-- again (#3066). Down is for development only.

DROP TRIGGER IF EXISTS audit_period_frozen_controls_immutable_trg ON audit_period_frozen_controls;
DROP FUNCTION IF EXISTS audit_period_frozen_controls_immutable();
DROP TABLE IF EXISTS audit_period_frozen_controls;
