-- security-atlas — slice 278: demo-seed UI button meta-audit.
--
-- Extends the action CHECK constraint on `me_audit_log` and
-- `super_admin_audit_log` to admit two new HTTP-invocation action
-- values used by the slice-278 `/v1/admin/demo/{seed,teardown}`
-- handlers:
--
--   - `demo_seed`     — written by the HTTP handler when an admin
--                       clicks the "Reseed demo dataset" button.
--                       Distinct from the seeder-run action
--                       `demo_seed_apply` written by
--                       internal/demoseed (slice 205).
--   - `demo_teardown` — written by the HTTP handler when an admin
--                       clicks the "Tear down demo tenant" button.
--                       Distinct from `demo_seed_teardown` written
--                       by internal/demoseed.
--
-- Forensic separation rationale: the HTTP-invocation row records
-- WHO CLICKED THE BUTTON (actor user_id + IP + timestamp). The
-- seeder-run rows record WHAT THE SEEDER DID (target tenant +
-- demo_seed_v stamp). Querying for `action IN ('demo_seed',
-- 'demo_teardown')` enumerates click events; querying for `action
-- IN ('demo_seed_apply', 'demo_seed_teardown')` enumerates seeder
-- runs. Two-row separation also tolerates the rare case where the
-- audit-row write succeeds but the seeder subprocess later fails.
--
-- LOAD-BEARING: this migration also RESTORES `demo_seed_apply` and
-- `demo_seed_teardown` to the CHECK list. The slice-269 migration
-- (20260524000000_dashboard_export_meta_audit.sql) inadvertently
-- dropped them when it rebuilt the CHECK constraint without
-- inheriting slice 205's additions. Without this restore, any new
-- `atlas-cli demo seed` invocation against a fresh DB would fail
-- with a check-constraint violation. The forward migration here
-- supersets all six values (the slice-269 baseline plus the four
-- demo-seed values).
--
-- Constitutional invariants honored:
--
--   #6 Tenant isolation via PostgreSQL RLS. The CHECK extension
--      does not alter RLS; demo_seed/demo_teardown rows are scoped
--      to the actor's session tenant_id (or NULL for the
--      super_admin_audit_log peer row) identically to every other
--      me_audit_log action.
--
--   Append-only ledger (canvas §4.3). The CHECK extension permits
--      new action values; no existing rows are altered. The
--      no-UPDATE/no-DELETE grant footprint on both tables is
--      unchanged.
--
-- Anti-criteria honored:
--
--   - P0-278-4 (does NOT skip audit-row write): the CHECK extension
--     unblocks the handler's pre-action audit-row insert.
--   - P0-278-8 (does NOT log seed dataset contents): the schema does
--     not constrain payload shape — that gate lives in the handler.
--
-- Reversible via 20260525000000_demo_seed_button_meta_audit.down.sql
-- which restores the slice-269 baseline (six values dropped).

-- ===== 1. me_audit_log.action CHECK extension =====

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
        -- Slice 205 (restored after slice 269 regression):
        'demo_seed_apply',
        'demo_seed_teardown',
        -- Slice 278 (HTTP-invocation events, distinct from seeder runs):
        'demo_seed',
        'demo_teardown'
    ));

-- ===== 2. super_admin_audit_log.action CHECK extension =====

ALTER TABLE super_admin_audit_log
    DROP CONSTRAINT IF EXISTS super_admin_audit_log_action_chk;

ALTER TABLE super_admin_audit_log
    ADD CONSTRAINT super_admin_audit_log_action_chk
    CHECK (action IN (
        'super_admin_grant',
        'super_admin_revoke',
        'tenant_create',
        -- Slice 205 (already present; preserved here for completeness):
        'demo_seed_apply',
        'demo_seed_teardown',
        -- Slice 278 (HTTP-invocation events):
        'demo_seed',
        'demo_teardown'
    ));
