-- #2985: ListInconclusiveStreaks finds each inconclusive cell's last
-- non-inconclusive evaluation. This partial index holds only those rows, so
-- the lookup is one probe however long the inconclusive run behind it is;
-- without it the read walks every row of the run. No row is written or
-- changed, and the table stays append-only.

CREATE INDEX IF NOT EXISTS idx_control_evaluations_not_inconclusive
    ON control_evaluations (tenant_id, control_id, scope_cell_id, evaluated_at DESC)
    WHERE result <> 'inconclusive';
