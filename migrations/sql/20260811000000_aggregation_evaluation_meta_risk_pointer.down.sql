-- Reverse #1867: restore the slice 054 fired-coherent CHECK, under which
-- only a `fired` row names a window and a meta-risk. A near_miss /
-- no_match row recorded since #1867 that names the meta-risk it refreshed
-- cannot satisfy it, so the down fails on such a row rather than
-- rewriting the append-only ledger; an operator decides what to do.

ALTER TABLE aggregation_rule_evaluations
    DROP CONSTRAINT IF EXISTS aggregation_rule_evaluations_fired_coherent;

ALTER TABLE aggregation_rule_evaluations
    ADD CONSTRAINT aggregation_rule_evaluations_fired_coherent
    CHECK (
        (outcome = 'fired' AND window_start IS NOT NULL AND meta_risk_id IS NOT NULL)
        OR (outcome <> 'fired' AND window_start IS NULL AND meta_risk_id IS NULL)
    );
