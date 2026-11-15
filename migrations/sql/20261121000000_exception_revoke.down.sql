-- security-atlas — reverse 20261121000000_exception_revoke.sql.
--
-- exception_audit_log is append-only audit evidence, so this reversal
-- refuses to run while any revoked exception or 'revoked' audit row
-- exists (the #2009 rule): the operator archives and removes them
-- deliberately first, or the narrowed CHECKs could not apply.

DO $$
DECLARE
    n bigint;
BEGIN
    SELECT (SELECT count(*) FROM exceptions WHERE status = 'revoked')
         + (SELECT count(*) FROM exception_audit_log WHERE action = 'revoked' OR to_state = 'revoked')
      INTO n;
    IF n > 0 THEN
        RAISE EXCEPTION 'exceptions hold % revoked row(s); refusing to delete audit evidence', n
            USING HINT = 'Archive and remove those rows deliberately before reversing 20261121000000.';
    END IF;
END
$$;

ALTER TABLE exceptions
    DROP CONSTRAINT IF EXISTS exceptions_revoked_has_actor;

ALTER TABLE exception_audit_log
    DROP CONSTRAINT IF EXISTS exception_audit_log_to_state_chk;

ALTER TABLE exception_audit_log
    ADD CONSTRAINT exception_audit_log_to_state_chk
        CHECK (to_state IN ('requested', 'approved', 'denied', 'active', 'expired'));

ALTER TABLE exception_audit_log
    DROP CONSTRAINT IF EXISTS exception_audit_log_action_chk;

ALTER TABLE exception_audit_log
    ADD CONSTRAINT exception_audit_log_action_chk
        CHECK (action IN ('requested', 'approved', 'denied', 'activated', 'expired'));

ALTER TABLE exceptions
    DROP CONSTRAINT IF EXISTS exceptions_status_chk;

ALTER TABLE exceptions
    ADD CONSTRAINT exceptions_status_chk
        CHECK (status IN ('requested', 'approved', 'denied', 'active', 'expired'));

ALTER TABLE exceptions
    DROP COLUMN IF EXISTS revoked_at,
    DROP COLUMN IF EXISTS revoked_by;
