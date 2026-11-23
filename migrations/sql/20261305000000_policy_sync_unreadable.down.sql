-- security-atlas -- reverse 20261305000000_policy_sync_unreadable.sql.
--
-- The unreadable state is derived: the next sync run records it again
-- from the pages it carries. Dropping it loses nothing a run cannot
-- rebuild.

ALTER TABLE policies
    DROP CONSTRAINT IF EXISTS policies_sync_unreadable_chk;

ALTER TABLE policies
    DROP COLUMN IF EXISTS sync_unreadable_reason,
    DROP COLUMN IF EXISTS sync_unreadable_since;
