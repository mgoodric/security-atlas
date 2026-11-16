-- security-atlas -- #2107: the risk open/closed lifecycle.
--
-- A risk is open or closed. Closing takes a reason, one of
-- not_applicable | merged | realised_and_handled, and an optional note;
-- reopening returns it to open and clears them. Closing retires a risk
-- without losing its history. Hard delete is admin-only and stays for
-- records that should never have existed.
--
-- 1. risks gains status (default open, so every existing risk is open),
--    the closure reason and note, and who closed it and when. A CHECK
--    keeps them consistent: an open risk carries no closure, and a closed
--    one carries a reason, a closer and a time.
--
-- 2. risk_audit_log accepts closed, reopened and deleted. This is the full
--    list from 20261206000000_risk_acceptance_transitions.sql plus the
--    three new values. risk_audit_log has no foreign key to risks and its
--    trigger refuses DELETE, so the row a hard delete writes outlives the
--    risk.

DO $$ BEGIN
    CREATE TYPE risk_status AS ENUM ('open', 'closed');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE risk_closure_reason AS ENUM (
        'not_applicable',
        'merged',
        'realised_and_handled'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

ALTER TABLE risks ADD COLUMN IF NOT EXISTS status risk_status NOT NULL DEFAULT 'open';
ALTER TABLE risks ADD COLUMN IF NOT EXISTS closure_reason risk_closure_reason NULL;
ALTER TABLE risks ADD COLUMN IF NOT EXISTS closure_note TEXT NOT NULL DEFAULT '';
ALTER TABLE risks ADD COLUMN IF NOT EXISTS closed_at TIMESTAMPTZ NULL;
ALTER TABLE risks ADD COLUMN IF NOT EXISTS closed_by TEXT NOT NULL DEFAULT '';

ALTER TABLE risks ADD CONSTRAINT risks_closure_consistent
    CHECK (
        (status = 'open'
            AND closure_reason IS NULL
            AND closure_note = ''
            AND closed_at IS NULL
            AND closed_by = '')
        OR
        (status = 'closed'
            AND closure_reason IS NOT NULL
            AND closed_at IS NOT NULL
            AND length(closed_by) > 0)
    );

ALTER TABLE risk_audit_log
    DROP CONSTRAINT risk_audit_log_action_type_check;

ALTER TABLE risk_audit_log
    ADD CONSTRAINT risk_audit_log_action_type_check
    CHECK (action_type IN (
        'placement_changed',
        'category_changed',
        'fields_changed',
        'treatment_changed',
        'acceptance_proposed',
        'acceptance_approved',
        'acceptance_rejected',
        'acceptance_revoked',
        'closed',
        'reopened',
        'deleted'
    ));
