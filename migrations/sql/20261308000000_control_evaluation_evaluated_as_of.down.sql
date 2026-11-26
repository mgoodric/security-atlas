-- Reverses 20261308000000_control_evaluation_evaluated_as_of.sql.

ALTER TABLE control_evaluations
    DROP CONSTRAINT IF EXISTS control_evaluations_as_of_before_evaluated;

ALTER TABLE control_evaluations
    DROP COLUMN IF EXISTS evaluated_as_of;
