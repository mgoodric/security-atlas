-- security-atlas — slice 269: dashboard snapshot export meta-audit.
--
-- One schema change: extend `me_audit_log.action` CHECK constraint to
-- permit the new `dashboard_export` value. Slice 269 records EVERY
-- dashboard-export attempt (success, 400, 403, 500) as a `me_audit_log`
-- row, mirroring the slice 135 / 137 / 138 / 174 / 175 pattern.
--
-- After slice 175 merged, the constraint permits the union of all
-- prior meta-audit actions (see
-- 20260522010000_controls_history_export_meta_audit.sql); this
-- migration adds exactly one new value (`dashboard_export`).
--
-- `dashboard_export` is the action value for the slice 269 snapshot
-- endpoint at `GET /v1/dashboard/export?format=json|csv|xlsx`. It is
-- distinct from every existing export action because the dashboard
-- export composes six panel reads into a single artifact — a forensic
-- query like
--   WHERE action = 'dashboard_export'
-- cleanly enumerates these multi-panel snapshots separately from the
-- single-domain exports.
--
-- Constitutional invariants honored:
--
--   #6 Tenant isolation via PostgreSQL RLS. The CHECK extension does
--      not alter RLS; slice 269's dashboard-export handler runs every
--      panel read under tenancy.ApplyTenant as atlas_app, identical to
--      slices 135 / 137 / 138 / 174 / 175.
--
--   Append-only ledger (canvas §4.3). The CHECK extension permits a
--      new READ-event action value in me_audit_log, which remains
--      append-only via its SELECT-+-INSERT-only RLS policy split.
--
-- Idempotency: ALTER TABLE ... DROP/ADD CONSTRAINT both succeed on
-- re-apply against an already-migrated database. Reversible via
-- 20260524000000_dashboard_export_meta_audit.down.sql.

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
        'tenant_create'
    ));
