-- security-atlas — slice 269: rollback for dashboard_export meta-audit action.
--
-- Removes the `dashboard_export` value from the me_audit_log.action
-- CHECK constraint, restoring the state established by slice 175's
-- migration (`20260522010000_controls_history_export_meta_audit.sql`).
--
-- Defensive DELETE — slice 269 follows the slice 175 D5 precedent:
-- even though `internal/api/dashboardexport/` is added to the CI
-- integration-test list in the same PR, a future test refactor could
-- collapse other surfaces that INSERT `dashboard_export` rows into
-- `me_audit_log`. The down migration must DELETE those rows BEFORE
-- the constraint swap or the new constraint check fails — cheap
-- insurance (slice 175 D5 / slice 137 D5 precedent).
--
-- Operators running this down in prod against retained forensics MUST
-- archive these rows separately before applying — the DELETE here is
-- correct under the CI workflow (ephemeral DB; no archival concern)
-- but is destructive in a prod-rollback context. Surface in CHANGELOG.

DELETE FROM me_audit_log WHERE action = 'dashboard_export';

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
        'risk_export',
        'controls_export',
        'controls_history_export',
        'evidence_export',
        'policies_export',
        'exceptions_export',
        'samples_export',
        'anchors_export',
        'tenant_rename',
        'bootstrap_first_install',
        'super_admin_grant',
        'super_admin_revoke',
        'tenant_create'
    ));
