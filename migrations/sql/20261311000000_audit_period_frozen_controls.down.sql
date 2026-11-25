-- security-atlas -- reverse 20261311000000_audit_period_frozen_controls.sql.
--
-- Drops the recorded control sets and their guards. Replay goes back to
-- reading the live catalog, so a control created or deleted after a freeze
-- breaks replay again (#3066). Down is for development only.

DROP TRIGGER IF EXISTS audit_periods_frozen_has_controls_trg ON audit_periods;
DROP FUNCTION IF EXISTS audit_periods_frozen_has_controls();
DROP FUNCTION IF EXISTS audit_period_record_catalog_controls(UUID, UUID, TEXT);
DROP TRIGGER IF EXISTS audit_period_frozen_controls_insert_guard_trg ON audit_period_frozen_controls;
DROP FUNCTION IF EXISTS audit_period_frozen_controls_insert_guard();
DROP TRIGGER IF EXISTS audit_period_frozen_controls_immutable_trg ON audit_period_frozen_controls;
DROP FUNCTION IF EXISTS audit_period_frozen_controls_immutable();
DROP TABLE IF EXISTS audit_period_frozen_controls;
