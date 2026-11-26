-- Reverse #1746's acceptance status. Restores the slice-019 accept CHECK and
-- the slice-002 column default. A proposed acceptance (no accepter or
-- expiry) cannot satisfy the restored CHECK, so the down fails on such a
-- row rather than rewriting it; an operator decides what it becomes. The
-- `untreated` enum value stays (Postgres cannot drop one); init.down.sql
-- drops the type.

ALTER TABLE risks DROP CONSTRAINT IF EXISTS risks_acceptance_status_iff_accept;
ALTER TABLE risks DROP CONSTRAINT IF EXISTS risks_accept_fields_required;
ALTER TABLE risks ADD CONSTRAINT risks_accept_fields_required
    CHECK (
        treatment <> 'accept'
        OR (accepted_until IS NOT NULL AND length(accepter) > 0)
    );
ALTER TABLE risks ALTER COLUMN treatment SET DEFAULT 'accept';
ALTER TABLE risks DROP COLUMN IF EXISTS acceptance_status;
DROP TYPE IF EXISTS risk_acceptance_status;
