-- security-atlas -- #2877: property drift on synced policies (Confluence
-- policy sync, #2813).
--
-- A sync run whose page Page Properties changed without a version bump
-- writes no version (Matt, 2026-11-21: flag as drift, apply nothing in
-- place). It records which properties moved on the page's published
-- synced row, beside the body drift of #2851 and separate from it:
--
--   sync_property_drift_detected_at  when a run first saw a property differ.
--   sync_property_drift_keys         the page property names that differ
--                                    now, sorted (title, approved_by_name,
--                                    approved_at, effective_date,
--                                    next_review_at, owner_role,
--                                    approver_role,
--                                    acknowledgment_required_roles).
--
-- Both are NULL when the row's properties match its page. A later run
-- clears them when the properties match again, or when the page arrives
-- with a new version (the superseded row's drift is cleared).
--
-- Recorded only on synced rows, with the two columns together and at
-- least one key.
--
-- Constitutional invariant #6: policies keeps its RLS policies; this
-- migration adds no table.
--
-- Reversible via 20261296000000_policy_sync_property_drift.down.sql.

ALTER TABLE policies
    ADD COLUMN sync_property_drift_detected_at TIMESTAMPTZ NULL,
    ADD COLUMN sync_property_drift_keys        TEXT[]      NULL;

ALTER TABLE policies
    ADD CONSTRAINT policies_sync_property_drift_chk
        CHECK (
            (sync_property_drift_detected_at IS NULL AND sync_property_drift_keys IS NULL)
            OR (
                source_attribution = 'synced'
                AND sync_property_drift_detected_at IS NOT NULL
                AND sync_property_drift_keys IS NOT NULL
                AND cardinality(sync_property_drift_keys) > 0
            )
        );
