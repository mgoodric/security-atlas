-- security-atlas — slice 137: controls UCF graph data export.
--
-- One schema change: extend `me_audit_log.action` CHECK constraint to
-- permit the new `controls_export` value. Slice 137 records EVERY
-- controls-export attempt (success, 400, 403, 413, 429, 500) as a
-- `me_audit_log` row, mirroring the slice 135 / 136 / 139 pattern.
--
-- After slice 136 + 139 merged, the constraint already permits
--   ('profile.update', 'preferences.update', 'session.revoke',
--    'audit_log_query_unified', 'audit_log_export',
--    'audit_periods_export', 'vendors_export', 'risk_export')
-- so this extension adds exactly one new value (`controls_export`).
--
-- `controls_export` is distinct from `risk_export`, `audit_periods_export`,
-- `vendors_export`, and `audit_log_export` so a forensic query like
-- `WHERE action = 'controls_export'` cleanly enumerates UCF / control
-- catalog extraction events. Different entity, different downstream
-- consumer (control exports feed compliance gap analysis + auditor
-- handoff index sheets), so distinct labels are correct.
--
-- Constitutional invariants honored:
--
--   #6 Tenant isolation via PostgreSQL RLS. The CHECK extension does
--      not alter RLS; slice 137's controls-export endpoint runs every
--      query under tenancy.ApplyTenant as atlas_app, identical to
--      slices 135 / 136 / 139.
--
--   Append-only ledger (canvas §4.3). The CHECK extension permits a
--      new READ-event action value in me_audit_log, which remains
--      append-only via its SELECT-+-INSERT-only RLS policy split.
--
-- Idempotency: ALTER TABLE ... DROP/ADD CONSTRAINT both succeed on
-- re-apply against an already-migrated database. Reversible via
-- 20260520000000_controls_export_meta_audit.down.sql.

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
