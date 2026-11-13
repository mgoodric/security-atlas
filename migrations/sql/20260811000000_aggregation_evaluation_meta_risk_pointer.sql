-- security-atlas -- #1867: a below-threshold evaluation may name the
-- meta-risk it refreshed.
--
-- Since #1861 an evaluation that misses its threshold still refreshes the
-- activation's existing meta-risk (relinks it to the current window and
-- recomputes its score). The fired-coherent CHECK only allowed
-- window_start + meta_risk_id on a `fired` row, so the near_miss /
-- no_match row that recorded that refresh could not say which meta-risk
-- changed.
--
-- The new CHECK keeps the old rule for `fired` (both set) and lets a
-- non-fired row carry both or neither: both when it refreshed a meta-risk,
-- neither when there was none to refresh. Every existing row already
-- satisfies it (each non-fired row has both NULL), so it is strictly
-- weaker than the constraint it replaces and no row is rewritten. It is
-- added NOT VALID and validated separately, so the validation scan holds
-- only a SHARE UPDATE EXCLUSIVE lock, not the ACCESS EXCLUSIVE of ADD.

ALTER TABLE aggregation_rule_evaluations
    DROP CONSTRAINT aggregation_rule_evaluations_fired_coherent;

ALTER TABLE aggregation_rule_evaluations
    ADD CONSTRAINT aggregation_rule_evaluations_fired_coherent
    CHECK (
        (window_start IS NULL) = (meta_risk_id IS NULL)
        AND (outcome <> 'fired' OR meta_risk_id IS NOT NULL)
    ) NOT VALID;

ALTER TABLE aggregation_rule_evaluations
    VALIDATE CONSTRAINT aggregation_rule_evaluations_fired_coherent;
