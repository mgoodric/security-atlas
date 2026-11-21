-- security-atlas -- #2851: drift on synced policies (Confluence policy sync, #2813).
--
-- A sync run whose page body changed without a version bump writes no
-- version. It records the drift on the page's published synced row
-- instead, so the policy shows that its source has moved on:
--
--   sync_drift_detected_at   when a run first saw the changed body.
--   sync_drift_body_sha256   hex sha256 of the body the source holds now.
--
-- Both are NULL when the row is in step with its page. A later run clears
-- them when the body matches synced_body_sha256 again, or when the page
-- arrives with a new version (the new version is a new row, and the
-- superseded row's drift is cleared).
--
-- Drift is recorded only on synced rows: the CHECK forbids it on any
-- other row, and requires the two columns together.
--
-- Constitutional invariant #6: policies keeps its RLS policies; this
-- migration adds no table.
--
-- Reversible via 20261280000000_policy_sync_drift.down.sql.

ALTER TABLE policies
    ADD COLUMN sync_drift_detected_at TIMESTAMPTZ NULL,
    ADD COLUMN sync_drift_body_sha256 TEXT        NULL;

ALTER TABLE policies
    ADD CONSTRAINT policies_sync_drift_chk
        CHECK (
            (sync_drift_detected_at IS NULL AND sync_drift_body_sha256 IS NULL)
            OR (
                source_attribution = 'synced'
                AND sync_drift_detected_at IS NOT NULL
                AND sync_drift_body_sha256 IS NOT NULL
                AND sync_drift_body_sha256 ~ '^[0-9a-f]{64}$'
            )
        );
