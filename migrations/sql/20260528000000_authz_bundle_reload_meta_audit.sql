-- security-atlas — slice 378: authz bundle hot-reload meta-audit.
--
-- Extends the action CHECK constraint on `me_audit_log` and
-- `super_admin_audit_log` to admit the new action value used by the
-- slice-378 `POST /v1/admin/authz-bundle/reload` handler:
--
--   - `authz_bundle_reload` — written by the HTTP handler on every
--                              successful super_admin-driven reload
--                              of the embedded authz bundle. The
--                              payload_json captures the before /
--                              after bundle SHA-256 so the audit
--                              trail records WHAT CHANGED in
--                              addition to WHO RELOADED.
--
-- Per slice 142 pattern, the handler dual-writes:
--
--   1. one `super_admin_audit_log` row (platform-global, since the
--      authz bundle is platform-global — not tenant-scoped at v1)
--   2. one `me_audit_log` row anchored to the actor's session tenant
--      so the slice-124 unified aggregator surfaces the event to
--      that tenant's admins/auditors
--
-- LOAD-BEARING: this migration is a strict superset of the slice-278
-- CHECK list — every prior admitted action stays admitted. Dropping
-- any existing value would break in-flight rows.
--
-- Constitutional invariants honored:
--
--   #6 Tenant isolation via PostgreSQL RLS. The CHECK extension does
--      not alter RLS; me_audit_log rows for authz_bundle_reload are
--      scoped to the actor's session tenant_id identically to every
--      other me_audit_log action. The super_admin_audit_log row is
--      platform-global (no RLS) per slice 142 D1.
--
--   Append-only ledger (canvas §4.3). The CHECK extension permits
--      a new action value; no existing rows are altered. The
--      no-UPDATE/no-DELETE grant footprint on both tables is
--      unchanged.
--
-- Anti-criteria honored:
--
--   - P0-378-1 (does NOT introduce torn-read window): schema-level
--     change only; the atomic.Pointer contract lives in the Go code.
--   - P0-378-5 (does NOT modify regocache): no touches.
--
-- Reversible via 20260528000000_authz_bundle_reload_meta_audit.down.sql
-- which restores the slice-278 baseline (drops `authz_bundle_reload`).

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
        'demo_seed_apply',
        'demo_seed_teardown',
        'demo_seed',
        'demo_teardown',
        -- Slice 378 (authz bundle hot-reload):
        'authz_bundle_reload'
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
        'demo_seed_apply',
        'demo_seed_teardown',
        'demo_seed',
        'demo_teardown',
        -- Slice 378 (authz bundle hot-reload):
        'authz_bundle_reload'
    ));
