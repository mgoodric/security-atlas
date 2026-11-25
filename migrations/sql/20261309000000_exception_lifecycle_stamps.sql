-- security-atlas -- #3056: a lifecycle state carries the stamp that
-- dates it.
--
-- Replay (#3037, ListExceptionsByControlAsOf) applies an exception at T
-- only when activated_at <= T, and the board brief aggregate (#3059) and
-- the OSCAL AR read activated_at, expired_at and revoked_at too. Nothing
-- in the schema made those stamps present. An active row with a NULL
-- activated_at was excepted live and never on replay; an expired row with
-- a NULL expired_at had no end the brief could count.
--
--   exceptions_activated_has_stamp  active, expired and revoked rows were
--                                   activated, so they carry activated_at.
--   exceptions_expired_has_stamp    an expired row carries expired_at.
--
-- A revoked row already carries revoked_at (exceptions_revoked_has_actor,
-- 20261127000000).
--
-- Existing rows missing a stamp take it from the exception_audit_log row
-- that recorded the transition, the only record of when it happened. A row
-- with no such audit row is not given an invented stamp: the migration
-- fails and says how many rows need one.

UPDATE exceptions e
SET activated_at = (
    SELECT min(l.occurred_at) FROM exception_audit_log l
    WHERE l.tenant_id = e.tenant_id AND l.exception_id = e.id
      AND l.action = 'activated')
WHERE e.activated_at IS NULL
  AND e.status IN ('active', 'expired', 'revoked');

UPDATE exceptions e
SET expired_at = (
    SELECT min(l.occurred_at) FROM exception_audit_log l
    WHERE l.tenant_id = e.tenant_id AND l.exception_id = e.id
      AND l.action = 'expired')
WHERE e.expired_at IS NULL
  AND e.status = 'expired';

DO $$
DECLARE
    missing bigint;
BEGIN
    SELECT count(*) INTO missing FROM exceptions
    WHERE (status IN ('active', 'expired', 'revoked') AND activated_at IS NULL)
       OR (status = 'expired' AND expired_at IS NULL);
    IF missing > 0 THEN
        RAISE EXCEPTION '#3056: % exception row(s) are active, expired or revoked with no activated_at or expired_at, and no audit row dates the transition; stamp them before applying this migration', missing;
    END IF;
END
$$;

ALTER TABLE exceptions
    DROP CONSTRAINT IF EXISTS exceptions_activated_has_stamp;

ALTER TABLE exceptions
    ADD CONSTRAINT exceptions_activated_has_stamp
        CHECK (status NOT IN ('active', 'expired', 'revoked') OR activated_at IS NOT NULL);

ALTER TABLE exceptions
    DROP CONSTRAINT IF EXISTS exceptions_expired_has_stamp;

ALTER TABLE exceptions
    ADD CONSTRAINT exceptions_expired_has_stamp
        CHECK (status <> 'expired' OR expired_at IS NOT NULL);
