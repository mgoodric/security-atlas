-- security-atlas — slice 136: rollback for risk_export meta-audit action.
--
-- Removes the `risk_export` value from the me_audit_log.action CHECK
-- constraint, restoring the state established by slice 139's migration
-- (which is the previous timestamped migration in the chain). When
-- slice 139's down then runs, it removes its own `audit_periods_export`
-- + `vendors_export` values, restoring the post-135 baseline.
--
-- Unlike slice 139's down (whose handlers live in `internal/api/admin*`
-- packages not exercised by the CI integration-test set), slice 136's
-- handler lives in `internal/api/risks/` which IS in the integration-
-- test list. CI integration tests therefore populate `risk_export`
-- rows in `me_audit_log` BEFORE the round-trip step runs. We must
-- delete those rows here or the CHECK constraint re-add will fail
-- against the now-disallowed action value.
--
-- Operators running this down in prod against retained forensics MUST
-- archive these rows separately before applying — the DELETE here is
-- correct under the CI workflow (ephemeral DB; no archival concern)
-- but is destructive in a prod-rollback context. Surface in CHANGELOG.

DELETE FROM me_audit_log WHERE action = 'risk_export';

ALTER TABLE me_audit_log
    DROP CONSTRAINT IF EXISTS me_audit_log_action_check;

ALTER TABLE me_audit_log
    ADD CONSTRAINT me_audit_log_action_check
    CHECK (action IN (
        'profile.update',
        'preferences.update',
        'session.revoke',
        'audit_log_query_unified',
        'audit_log_export',
        'audit_periods_export',
        'vendors_export'
    ));
