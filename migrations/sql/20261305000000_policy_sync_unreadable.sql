-- security-atlas -- #3000: unreadable pages on synced policies (Confluence policy sync, #2813).
--
-- A sync run may carry a page as unreadable: the page is still labelled
-- in its source, but the connector could not read its body (the
-- Confluence converter refused it as too deep or too large, #2943). Such
-- a page is present, so it is not missing (#2853), and nothing about its
-- policy changes: no body, hash, property or version is written. The run
-- records the state on the page's published synced row instead:
--
--   sync_unreadable_since    when a run first carried the page unreadable.
--   sync_unreadable_reason   the pusher's reason from the latest such run,
--                            at most 500 characters.
--
-- Both NULL when the latest run carried the page readable, or did not
-- carry it at all. GET /v1/needs-attention lists every published synced
-- policy with them set.
--
-- Allowed only on synced rows, and set together: the CHECKs forbid them
-- on any other row and forbid one without the other.
--
-- Constitutional invariant #6: policies keeps its RLS policies; this
-- migration adds no table.
--
-- Reversible via 20261305000000_policy_sync_unreadable.down.sql.

ALTER TABLE policies
    ADD COLUMN sync_unreadable_since TIMESTAMPTZ NULL,
    ADD COLUMN sync_unreadable_reason TEXT NULL;

ALTER TABLE policies
    ADD CONSTRAINT policies_sync_unreadable_chk
        CHECK (
            (sync_unreadable_since IS NULL AND sync_unreadable_reason IS NULL)
            OR (
                sync_unreadable_since IS NOT NULL
                AND sync_unreadable_reason IS NOT NULL
                AND source_attribution = 'synced'
                AND char_length(sync_unreadable_reason) BETWEEN 1 AND 500
            )
        );
