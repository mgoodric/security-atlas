-- Reverses 20261252000000_me_audit_log_actor_principal.sql (#2329).

ALTER TABLE me_audit_log
    DROP CONSTRAINT IF EXISTS me_audit_log_actor_principal_nonempty;

ALTER TABLE me_audit_log
    DROP COLUMN IF EXISTS actor_principal;
