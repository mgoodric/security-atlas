-- Reverse #1880: drop the column comments. The columns carried none
-- before it.

COMMENT ON COLUMN aggregation_rule_evaluations.window_start IS NULL;
COMMENT ON COLUMN aggregation_rule_evaluations.meta_risk_id IS NULL;
