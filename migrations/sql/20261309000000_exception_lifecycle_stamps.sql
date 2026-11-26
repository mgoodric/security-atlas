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
-- Backfill of existing rows missing a stamp, in order of preference (D-log
-- docs/audit-log/3037-replay-exceptions-as-of-decisions.md D4, after the
-- #2078 precedent in 20261258000000_framework_scope_activated_at.sql):
--   1. the exception_audit_log row that recorded the transition
--      (`activated`, `expired`), the recorded time;
--   2. otherwise a documented stand-in, so the upgrade never refuses to
--      run. The demo seeder wrote `expired` rows with no stamps and no
--      audit rows before #3056, and every install that ran it has them.
--        activated_at := COALESCE(effective_from, approved_at, requested_at)
--        expired_at   := LEAST(expires_at, updated_at)
--      activated_at is the earliest lifecycle time the row records; the
--      sweep expires a row only after its expires_at, and updated_at is at
--      or after the expiry write.
-- A NOTICE gives how many rows took a stand-in. Every UPDATE touches only
-- rows whose stamp is NULL, so re-running the migration is a no-op.

DO $$
DECLARE
    fallback_activated bigint;
    fallback_expired   bigint;
BEGIN
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

    UPDATE exceptions
    SET activated_at = COALESCE(effective_from, approved_at, requested_at)
    WHERE activated_at IS NULL
      AND status IN ('active', 'expired', 'revoked');
    GET DIAGNOSTICS fallback_activated = ROW_COUNT;

    UPDATE exceptions
    SET expired_at = LEAST(expires_at, updated_at)
    WHERE expired_at IS NULL
      AND status = 'expired';
    GET DIAGNOSTICS fallback_expired = ROW_COUNT;

    IF fallback_activated > 0 OR fallback_expired > 0 THEN
        RAISE NOTICE '#3056: no audit row dated % activated_at and % expired_at stamp(s); set from effective_from/approved_at/requested_at and LEAST(expires_at, updated_at)',
            fallback_activated, fallback_expired;
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
