-- security-atlas — slice 174: UCF anchor catalog export.
--
-- One schema change: extend `me_audit_log.action` CHECK constraint to
-- permit the new `anchors_export` value. Slice 174 records EVERY
-- anchor-catalog export attempt (success, 400, 403, 413, 429, 500) as
-- a `me_audit_log` row, mirroring the slice 135 / 136 / 137 / 138 / 139
-- pattern.
--
-- After slice 138 merged, the constraint permits
--   ('profile.update', 'preferences.update', 'session.revoke',
--    'audit_log_query_unified', 'audit_log_export',
--    'audit_periods_export', 'vendors_export', 'risk_export',
--    'controls_export',
--    'evidence_export', 'policies_export', 'exceptions_export',
--    'samples_export')
-- so this extension adds exactly one new value (`anchors_export`).
--
-- `anchors_export` is the slice 174 D2 plural value (matches slice 137
-- `controls_export`, slice 138 `evidence_export`/etc, slice 139
-- `audit_periods_export`/`vendors_export`). Slice 136's singular
-- `risk_export` (one register per tenant) remains the outlier.
--
-- Why a distinct action: a forensic query like
--   WHERE action = 'anchors_export'
-- cleanly enumerates SCF catalog extraction events. Anchor exports are
-- a different downstream consumer than control exports (the anchor
-- catalog is global, public-domain data; the consumers are typically
-- auditor-handoff bundles and vendor-due-diligence packs).
--
-- Constitutional invariants honored:
--
--   #6 Tenant isolation via PostgreSQL RLS. The CHECK extension does
--      not alter RLS; slice 174's anchors-export endpoint runs the
--      meta-audit insert under tenancy.ApplyTenant as atlas_app,
--      identical to slices 135 / 136 / 137 / 138 / 139. Note: the
--      SCF anchor catalog reads themselves are public-domain (no
--      tenant_id, no RLS); only the meta-audit row is tenant-scoped.
--
--   Append-only ledger (canvas §4.3). The CHECK extension permits a
--      new READ-event action value in me_audit_log, which remains
--      append-only via its SELECT-+-INSERT-only RLS policy split.
--
-- Idempotency: ALTER TABLE ... DROP/ADD CONSTRAINT both succeed on
-- re-apply against an already-migrated database. Reversible via
-- 20260520010000_anchors_export_meta_audit.down.sql.

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
        'evidence_export',
        'policies_export',
        'exceptions_export',
        'samples_export',
        'anchors_export'
    ));
