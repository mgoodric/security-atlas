-- security-atlas -- #1746: untreated default + acceptance status.
--
-- 1. risks.treatment defaults to `untreated` (added by
--    20260809000000_risk_treatment_untreated.sql). The old column default
--    was `accept`, which the accept CHECK made unusable anyway.
--
-- 2. An `accept` treatment carries an acceptance status, proposed |
--    approved. A proposed acceptance (pending sign-off) needs no accepter
--    or expiry. An approved acceptance requires both, as every accept did
--    before. acceptance_status is set exactly when treatment = 'accept'.
--
-- Existing rows: every current `accept` row already satisfies
-- risks_accept_fields_required (accepter + accepted_until present), so it
-- is backfilled as `approved`, which is what it was. Rows with any other
-- treatment keep acceptance_status NULL. No treatment value is rewritten.

DO $$ BEGIN
    CREATE TYPE risk_acceptance_status AS ENUM (
        'proposed',
        'approved'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

ALTER TABLE risks ADD COLUMN acceptance_status risk_acceptance_status NULL;

UPDATE risks SET acceptance_status = 'approved' WHERE treatment = 'accept';

ALTER TABLE risks ALTER COLUMN treatment SET DEFAULT 'untreated';

-- The accept-fields rule now binds only an approved acceptance.
ALTER TABLE risks DROP CONSTRAINT risks_accept_fields_required;
ALTER TABLE risks ADD CONSTRAINT risks_accept_fields_required
    CHECK (
        acceptance_status IS DISTINCT FROM 'approved'
        OR (accepted_until IS NOT NULL AND length(accepter) > 0)
    );

ALTER TABLE risks ADD CONSTRAINT risks_acceptance_status_iff_accept
    CHECK ((treatment = 'accept') = (acceptance_status IS NOT NULL));
