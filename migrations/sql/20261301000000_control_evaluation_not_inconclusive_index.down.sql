-- Reverse #2985's partial index. No data depends on it.
DROP INDEX IF EXISTS idx_control_evaluations_not_inconclusive;
