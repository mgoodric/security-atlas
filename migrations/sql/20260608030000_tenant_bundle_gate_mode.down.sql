-- Reverse slice 608 — drop the per-tenant control-bundle gate-policy column
-- and restore the me_audit_log.action allow-list to the slice-478 baseline.
--
-- After this runs the upload gate falls back to the slice-574 global hard-block
-- default (the resolver treats a missing column as 'strict').
--
-- Append-only contract preserved: tightening the CHECK does not modify existing
-- rows. If any 'tenant_gate_policy_update' rows were written before rollback,
-- the operator removes them manually (a schema down-migration does not delete
-- data) — same convention as the slice-478 down migration.

-- ===== 1. me_audit_log.action CHECK restore (slice-478 baseline) =====

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
        'dashboard_export',
        'tenant_rename',
        'bootstrap_first_install',
        'super_admin_grant',
        'super_admin_revoke',
        'tenant_create',
        'demo_seed_apply',
        'demo_seed_teardown',
        'demo_seed',
        'demo_teardown',
        'authz_bundle_reload',
        'user_tenant_assign',
        'user_tenant_revoke'
    ));

-- ===== 2. drop the column + its CHECK =====

ALTER TABLE tenants
    DROP CONSTRAINT IF EXISTS tenants_bundle_gate_mode_chk;

ALTER TABLE tenants
    DROP COLUMN IF EXISTS bundle_gate_mode;
