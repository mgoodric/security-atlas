-- security-atlas -- reverse 20261297000000_policy_sync_property_drift.sql.
--
-- Property drift is derived state: the next sync run records it again
-- from the page. Dropping it loses nothing a run cannot rebuild.

ALTER TABLE policies
    DROP CONSTRAINT IF EXISTS policies_sync_property_drift_chk;

ALTER TABLE policies
    DROP COLUMN IF EXISTS sync_property_drift_keys,
    DROP COLUMN IF EXISTS sync_property_drift_detected_at;
