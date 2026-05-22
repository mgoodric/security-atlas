-- security-atlas — slice 175: control bundle history export meta-audit.
--
-- One schema change: extend `me_audit_log.action` CHECK constraint to
-- permit the new `controls_history_export` value. Slice 175 records
-- EVERY history-export attempt (success, 400, 403, 413, 429, 500) as a
-- `me_audit_log` row, mirroring the slice 135 / 136 / 137 / 138 / 139 /
-- 174 pattern.
--
-- After slice 143 merged, the constraint permits the union of all
-- prior meta-audit actions (see 20260522000000_tenants_slug_create_flow.sql
-- lines 200–221); this migration adds exactly one new value
-- (`controls_history_export`).
--
-- `controls_history_export` is distinct from slice 137's
-- `controls_export` so a forensic query like
--   WHERE action = 'controls_history_export'
-- cleanly enumerates lineage-export events. The two consumers are
-- different: slice 137's active-only export feeds compliance gap
-- analysis; slice 175's history export feeds auditor period-freeze
-- reconstruction ("what did this control look like at frozen_at T?").
--
-- Constitutional invariants honored:
--
--   #6 Tenant isolation via PostgreSQL RLS. The CHECK extension does
--      not alter RLS; slice 175's history-export endpoint runs every
--      query under tenancy.ApplyTenant as atlas_app, identical to
--      slices 135 / 136 / 137 / 138 / 139 / 174.
--
--   Append-only ledger (canvas §4.3). The CHECK extension permits a
--      new READ-event action value in me_audit_log, which remains
--      append-only via its SELECT-+-INSERT-only RLS policy split.
--
-- Idempotency: ALTER TABLE ... DROP/ADD CONSTRAINT both succeed on
-- re-apply against an already-migrated database. Reversible via
-- 20260522010000_controls_history_export_meta_audit.down.sql.

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
