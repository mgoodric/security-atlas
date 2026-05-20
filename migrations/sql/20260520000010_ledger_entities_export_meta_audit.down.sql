-- security-atlas — slice 138: rollback for ledger-entity export meta-audit actions.
--
-- Removes the four new action values (`evidence_export`,
-- `policies_export`, `exceptions_export`, `samples_export`) from the
-- me_audit_log.action CHECK constraint, restoring the state
-- established by slice 137's migration (which is the previous
-- timestamped migration in the chain at
-- `20260520000000_controls_export_meta_audit.sql`).
--
-- Defensive DELETE — slice 138 D5: even though the four target
-- packages (`internal/api/evidence/`, `internal/api/policies/`,
-- `internal/api/exceptions/`, `internal/api/audit/`) are NOT in the
-- current CI integration-test list (`.github/workflows/ci.yml` line
-- 289–310), unit-test fixtures or future integration tests could
-- INSERT these action values into me_audit_log. The down migration
-- must DELETE those rows BEFORE the constraint swap or the new
-- constraint check fails — slice 136's migration round-trip failed
-- three times for exactly this class of bug. Cheap insurance.
--
-- Operators running this down in prod against retained forensics MUST
-- archive these rows separately before applying — the DELETE here is
-- correct under the CI workflow (ephemeral DB; no archival concern)
-- but is destructive in a prod-rollback context. Surface in CHANGELOG.

DELETE FROM me_audit_log WHERE action IN (
    'evidence_export',
    'policies_export',
    'exceptions_export',
    'samples_export'
);

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
        'controls_export'
    ));
