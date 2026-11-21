-- security-atlas -- reverse 20261285000000_policy_sync_missing.sql.
--
-- The missing state is derived: the next sync run records it again from
-- the pages it carries. Dropping it loses nothing a run cannot rebuild.

ALTER TABLE policies
    DROP CONSTRAINT IF EXISTS policies_sync_missing_chk;

ALTER TABLE policies
    DROP COLUMN IF EXISTS sync_missing_since;
