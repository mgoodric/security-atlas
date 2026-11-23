-- security-atlas — #2957: risk import parent links across batches, stranded
-- waiting rows, and the export's parent_risk_ids.
--
-- risk_import_rows gains:
--
--   parent_risk_ids  the risks of the tenant the export's parent_risk_ids
--                    cell named, staged only when the row names no parent
--                    by organisation risk ID (parent_ref is ''). The row's
--                    risk is linked under each when it is approved; a
--                    linked row's parent_risk_id is the first of them.
--
-- parent_link_status 'waiting' now means the row is approved and its parent
-- is a pending row of any import batch of the tenant, not only its own
-- batch. A waiting row is settled when a row carrying its parent's ID is
-- decided, in whichever batch. When the row's own batch completes, a row
-- still waiting on an ID that no pending row of the tenant carries fails.
--
-- The waiting index moves from (batch_id, ...) to (tenant_id, ...) to match,
-- and the pending-parent check gets an index of its own.

ALTER TABLE risk_import_rows
    ADD COLUMN IF NOT EXISTS parent_risk_ids UUID[] NOT NULL DEFAULT '{}';

ALTER TABLE risk_import_rows
    DROP CONSTRAINT IF EXISTS risk_import_rows_parent_link_chk;
ALTER TABLE risk_import_rows
    ADD CONSTRAINT risk_import_rows_parent_link_chk
        CHECK (parent_link_status IN ('none', 'waiting', 'linked', 'failed')
               AND (parent_ref = '' OR cardinality(parent_risk_ids) = 0)
               AND (parent_ref <> '' OR cardinality(parent_risk_ids) > 0 OR parent_link_status = 'none')
               AND (parent_link_status <> 'waiting' OR parent_ref <> '')
               AND (parent_link_status = 'none' OR status = 'approved')
               AND ((parent_link_status = 'linked') = (parent_risk_id IS NOT NULL))
               AND ((parent_link_status = 'failed') = (parent_link_error <> '')));

DROP INDEX IF EXISTS risk_import_rows_waiting_parent_idx;
CREATE INDEX IF NOT EXISTS risk_import_rows_waiting_parent_idx
    ON risk_import_rows (tenant_id, lower(parent_ref))
    WHERE parent_link_status = 'waiting';

-- The pending rows of the tenant carrying an organisation risk ID, read
-- when a child's parent is not yet a risk.
CREATE INDEX IF NOT EXISTS risk_import_rows_pending_ref_idx
    ON risk_import_rows (tenant_id, lower(external_ref))
    WHERE status = 'pending' AND external_ref IS NOT NULL;
