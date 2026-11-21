-- security-atlas -- reverse 20261280000000_policy_sync_drift.sql.
--
-- Drift is derived state: the next sync run records it again from the
-- page. Dropping it loses nothing a run cannot rebuild.

ALTER TABLE policies
    DROP CONSTRAINT IF EXISTS policies_sync_drift_chk;

ALTER TABLE policies
    DROP COLUMN IF EXISTS sync_drift_body_sha256,
    DROP COLUMN IF EXISTS sync_drift_detected_at;
