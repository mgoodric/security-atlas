-- security-atlas — #2846: parent links on staged risk import rows (spec
-- #2812, Q17 / Q29).
--
-- risk_import_rows gains the row's parent-risk-ID cell and the outcome of
-- linking the row's risk under that parent:
--
--   parent_ref          the parent-risk-ID cell as written, '' when blank.
--                       Resolved by organisation risk ID, ignoring case,
--                       against the tenant's risks and this batch's rows;
--   parent_link_status  none     no link to make: no parent ref, or the row
--                                is not approved;
--                       waiting  the row is approved and its parent is a
--                                pending row of this batch; the link is made
--                                when that row is approved;
--                       linked   the link is in risk_aggregations;
--                       failed   the parent is unknown, or the link broke a
--                                hierarchy rule (a cycle, a child not below
--                                its parent's level, a child not 5x5-scored).
--                                The row also carries the parent_link_failed
--                                flag, and the risk itself is imported;
--   parent_link_error   why the link failed, '' otherwise;
--   parent_risk_id      the parent risk a linked row was linked under.
--
-- No foreign key on parent_risk_id: the link itself lives in
-- risk_aggregations, which cascades with either risk; this column records
-- the import's outcome, as risk_id does.

ALTER TABLE risk_import_rows
    ADD COLUMN IF NOT EXISTS parent_ref         TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS parent_link_status TEXT NOT NULL DEFAULT 'none',
    ADD COLUMN IF NOT EXISTS parent_link_error  TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS parent_risk_id     UUID NULL;

ALTER TABLE risk_import_rows
    DROP CONSTRAINT IF EXISTS risk_import_rows_parent_link_chk;
ALTER TABLE risk_import_rows
    ADD CONSTRAINT risk_import_rows_parent_link_chk
        CHECK (parent_link_status IN ('none', 'waiting', 'linked', 'failed')
               AND (parent_ref <> '' OR parent_link_status = 'none')
               AND (parent_link_status = 'none' OR status = 'approved')
               AND ((parent_link_status = 'linked') = (parent_risk_id IS NOT NULL))
               AND ((parent_link_status = 'failed') = (parent_link_error <> '')));

-- The rows waiting on a parent row of the batch, found when that row is
-- decided.
CREATE INDEX IF NOT EXISTS risk_import_rows_waiting_parent_idx
    ON risk_import_rows (batch_id, lower(parent_ref))
    WHERE parent_link_status = 'waiting';
