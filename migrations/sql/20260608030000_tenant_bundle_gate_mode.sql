-- security-atlas — slice 608: per-tenant control-bundle upload test-gate policy.
--
-- Slice 574 wired the control-bundle upload test-gate into
-- `POST /v1/controls:upload-bundle` with a single GLOBAL v0 policy
-- (decisions log 574 D-POLICY-1/2): hard-block when a bundle ships tests/ and
-- any case is red; allow-with-warning when a bundle ships no tests. Slice 574
-- deferred the per-tenant flag (D-POLICY-3) precisely because it needs this
-- column + a settings surface.
--
-- This migration adds that per-tenant policy as ONE column on the existing
-- `tenants` identity row (slice 144). A single TEXT enum covers both opt-in
-- dimensions slice 574 surfaced (see docs/audit-log/608-*-decisions.md D1):
--
--   'strict'           - DEFAULT. Preserves the slice-574 global behaviour
--                        exactly: hard-block a bundle with red tests; allow a
--                        bundle with no tests/ (with a warning). A tenant that
--                        does nothing keeps the safe default — no backfill.
--   'advisory'         - accept a bundle with red tests but attach the per-case
--                        report as a warning (for tenants authoring iteratively
--                        who want the gate's feedback without a hard block).
--   'mandatory_tests'  - reject a bundle that ships NO tests/ (the opposite of
--                        the strict no-tests allowance), for tenants who want
--                        every control test-backed. Red tests still hard-block.
--
-- Why a column on `tenants`, not the `feature_flags` table or a new table:
--   - `feature_flags` (slice 059) is a BOOLEAN-per-key store keyed to a fixed
--     Seed list; a three-valued policy enum does not fit its shape.
--   - `tenants` already is the per-tenant identity row, already under FORCE RLS
--     with the slice-002 four-policy pattern, and already has an admin-gated
--     write surface (`PATCH /v1/tenants/{id}`, slice 144). Reusing it is the
--     minimal home (slice doc "reuse existing tenant-settings plumbing").
--
-- Constitutional invariants honored:
--   #6  Tenant isolation at the DB layer. The column lives on `tenants`, whose
--       RLS already scopes every read/write to the caller's own row. No new
--       table, no new policy needed — the existing four-policy set covers it.
--
-- Resolution precedence (slice doc): an existing tenant row gets the column
-- value 'strict' via the DEFAULT, so existing tenants keep the slice-574
-- behaviour with zero backfill. NULL is impossible (NOT NULL + DEFAULT).
--
-- Reversible via 20260608030000_tenant_bundle_gate_mode.down.sql (drops the
-- column + its CHECK).

ALTER TABLE tenants
    ADD COLUMN IF NOT EXISTS bundle_gate_mode TEXT NOT NULL DEFAULT 'strict';

ALTER TABLE tenants
    DROP CONSTRAINT IF EXISTS tenants_bundle_gate_mode_chk;

ALTER TABLE tenants
    ADD CONSTRAINT tenants_bundle_gate_mode_chk
    CHECK (bundle_gate_mode IN ('strict', 'advisory', 'mandatory_tests'));

COMMENT ON COLUMN tenants.bundle_gate_mode IS
    'Slice 608: per-tenant control-bundle upload test-gate policy. strict (default, = slice-574 global behaviour) | advisory (red tests warn, not block) | mandatory_tests (a bundle with no tests/ is rejected). Set via PATCH /v1/tenants/{id}.';

-- ===== me_audit_log.action CHECK extension =====
--
-- PATCH /v1/tenants/{id} writes a me_audit_log row per successful gate-policy
-- change (same pattern as the slice-144 'tenant_rename' row). Add the new
-- 'tenant_gate_policy_update' action to the existing allow-list. The list below
-- mirrors the slice-478 form (20260607010000) plus the new value.

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
        'user_tenant_revoke',
        -- Slice 608 (per-tenant control-bundle gate policy):
        'tenant_gate_policy_update'
    ));
