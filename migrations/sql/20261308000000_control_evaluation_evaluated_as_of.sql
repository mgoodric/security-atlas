-- security-atlas -- #3056: the instant a replay row judged.
--
-- A replay of T (#3037) writes rows stamped evaluated_at = the clock of the
-- run, because evaluated_at orders the ledger and "latest state" reads it.
-- The instant the row was judged at -- the horizon its evidence and
-- exceptions were read as of -- was not recorded, so the OSCAL AR rebuilt a
-- row's waiving exception at evaluated_at and could find none: an excepted
-- replay row with no named exception.
--
-- evaluated_as_of records that instant. It is set only when the run judged
-- an earlier instant than evaluated_at (a replay); live rows stay NULL, and
-- a reader takes COALESCE(evaluated_as_of, evaluated_at) as the instant the
-- row describes.
--
-- Additive and nullable: no backfill (no earlier row recorded its horizon),
-- no rewrite of existing rows, and the table stays append-only.

ALTER TABLE control_evaluations
    ADD COLUMN IF NOT EXISTS evaluated_as_of TIMESTAMPTZ NULL;

ALTER TABLE control_evaluations
    DROP CONSTRAINT IF EXISTS control_evaluations_as_of_before_evaluated;

ALTER TABLE control_evaluations
    ADD CONSTRAINT control_evaluations_as_of_before_evaluated
        CHECK (evaluated_as_of IS NULL OR evaluated_as_of < evaluated_at);
