-- Reverses 20261238000000_control_evaluation_triggered_by.sql.

ALTER TABLE control_evaluations
    DROP CONSTRAINT IF EXISTS control_evaluations_triggered_by_nonempty;

ALTER TABLE control_evaluations
    DROP CONSTRAINT IF EXISTS control_evaluations_triggered_by_manual_only;

ALTER TABLE control_evaluations
    DROP COLUMN IF EXISTS triggered_by;
