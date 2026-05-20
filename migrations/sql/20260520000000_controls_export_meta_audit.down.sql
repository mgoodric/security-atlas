-- security-atlas — slice 137: rollback for controls_export meta-audit action.
--
-- Removes the `controls_export` value from the me_audit_log.action CHECK
-- constraint, restoring the state established by slice 136's migration
-- (which is the previous timestamped migration in the chain at
-- `20260519000010_risk_export_meta_audit.sql`).
--
-- Defensive DELETE — slice 137 D5: even though `internal/api/controls/`
-- is NOT in the current CI integration-test list
-- (`.github/workflows/ci.yml` line 289–310), the `internal/control/`
-- package IS, and a future test refactor that collapses the two could
-- surface integration tests that INSERT `controls_export` rows into
-- `me_audit_log`. The down migration must DELETE those rows BEFORE
-- the constraint swap or the new constraint check fails — slice 136's
-- migration round-trip failed three times for exactly this class of
-- bug. Cheap insurance.
--
-- Operators running this down in prod against retained forensics MUST
-- archive these rows separately before applying — the DELETE here is
-- correct under the CI workflow (ephemeral DB; no archival concern)
-- but is destructive in a prod-rollback context. Surface in CHANGELOG.

DELETE FROM me_audit_log WHERE action = 'controls_export';

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
        'vendors_export',
        'risk_export'
    ));
