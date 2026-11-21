-- security-atlas -- #2853: missing pages on synced policies (Confluence policy sync, #2813).
--
-- Each sync run pushes the full set of labelled pages for its source, so
-- a page absent from a run has lost its label or been deleted. Its
-- published synced policy stays in force: it is not superseded, hidden
-- or unpublished, and acknowledgements continue. The run records the
-- absence on the row instead:
--
--   sync_missing_since   when a run first found the page absent.
--
-- NULL when the page was in the latest run. A later run that carries the
-- page again clears it. GET /v1/needs-attention lists every published
-- synced policy with it set.
--
-- Allowed only on synced rows: the CHECK forbids it on any other row.
--
-- Constitutional invariant #6: policies keeps its RLS policies; this
-- migration adds no table.
--
-- Reversible via 20261286000000_policy_sync_missing.down.sql.

ALTER TABLE policies
    ADD COLUMN sync_missing_since TIMESTAMPTZ NULL;

ALTER TABLE policies
    ADD CONSTRAINT policies_sync_missing_chk
        CHECK (sync_missing_since IS NULL OR source_attribution = 'synced');
