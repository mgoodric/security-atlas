-- security-atlas — #2113: revoke an active exception before it expires.
--
-- An approver ends an active exception early, with a reason. Revoked is
-- terminal, like denied and expired: to waive the control again, file a
-- new exception. The evaluator reads only active exceptions
-- (ListExceptionsByControl), so a revoked one stops covering the control
-- on its next evaluation without any change there.
--
--   1. exceptions.revoked_by / revoked_at   who ended it and when. Same
--      shape as denied_by / denied_at.
--   2. exceptions_status_chk                gains 'revoked'.
--   3. exception_audit_log action / to_state gain 'revoked'. The reason
--      the approver gives is the audit row's reason.
--   4. exceptions_revoked_has_actor         a revoked row names who
--      revoked it and when, so the trail cannot lose the actor.
--
-- Every existing value is kept. Reversible via
-- 20261118000000_exception_revoke.down.sql, which refuses while a revoked
-- row exists (audit evidence is not deleted by a down migration).

-- ===== 1. who revoked it =====

ALTER TABLE exceptions
    ADD COLUMN IF NOT EXISTS revoked_by TEXT NULL,
    ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ NULL;

-- ===== 2. the state =====

ALTER TABLE exceptions
    DROP CONSTRAINT IF EXISTS exceptions_status_chk;

ALTER TABLE exceptions
    ADD CONSTRAINT exceptions_status_chk
        CHECK (status IN ('requested', 'approved', 'denied', 'active', 'expired', 'revoked'));

-- ===== 3. the audit vocabulary =====

ALTER TABLE exception_audit_log
    DROP CONSTRAINT IF EXISTS exception_audit_log_action_chk;

ALTER TABLE exception_audit_log
    ADD CONSTRAINT exception_audit_log_action_chk
        CHECK (action IN ('requested', 'approved', 'denied', 'activated', 'expired', 'revoked'));

ALTER TABLE exception_audit_log
    DROP CONSTRAINT IF EXISTS exception_audit_log_to_state_chk;

ALTER TABLE exception_audit_log
    ADD CONSTRAINT exception_audit_log_to_state_chk
        CHECK (to_state IN ('requested', 'approved', 'denied', 'active', 'expired', 'revoked'));

-- ===== 4. a revoked row names its actor =====

ALTER TABLE exceptions
    DROP CONSTRAINT IF EXISTS exceptions_revoked_has_actor;

ALTER TABLE exceptions
    ADD CONSTRAINT exceptions_revoked_has_actor
        CHECK (status <> 'revoked' OR (revoked_by IS NOT NULL AND revoked_at IS NOT NULL));
