-- security-atlas -- #3089: audit_periods_frozen_has_controls() is not
-- executable by PUBLIC.
--
-- 20261314000000 (#3085) made the function SECURITY DEFINER, owned by
-- atlas_migrate, but left the default PUBLIC EXECUTE in place. It is a
-- trigger function, so a direct call already fails ("trigger functions can
-- only be called as triggers"), and firing a trigger does not check
-- EXECUTE, so the deferred commit check runs for atlas_app exactly as
-- before. The revoke follows the rule that a SECURITY DEFINER function
-- grants nothing it does not need to: the owner keeps EXECUTE.
--
-- Constitutional invariants:
--   #2  nothing here writes a row.
--   #10 the frozen-period commit check is unchanged.
--
-- Reversible via 20261315000000_audit_period_frozen_has_controls_revoke.down.sql.

REVOKE EXECUTE ON FUNCTION audit_periods_frozen_has_controls() FROM PUBLIC;
