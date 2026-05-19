-- security-atlas — slice 136: risk register data export.
--
-- One schema change: extend `me_audit_log.action` CHECK constraint to
-- permit the new `risk_export` value. Slice 136 records EVERY risk-register
-- export attempt (success, 400, 403, 413, 500) as a `me_audit_log` row,
-- mirroring the slice 135 `audit_log_export` pattern. The existing CHECK
-- after slice 135 only allowed
-- ('profile.update', 'preferences.update', 'session.revoke',
-- 'audit_log_query_unified', 'audit_log_export') so this extension is
-- load-bearing.
--
-- `risk_export` is distinct from `audit_log_export` (slice 135's bulk-PII
-- read meta-audit action) so a forensic query like
-- `WHERE action = 'risk_export'` cleanly enumerates risk-register
-- extraction events. Different entity, different downstream consumer
-- (risk registers go to board packs / quarterly reports; audit logs go
-- to auditors), so they deserve distinct labels.
--
-- Constitutional invariants honored:
--
--   #6 Tenant isolation via PostgreSQL RLS. The CHECK extension does
--      not alter RLS; slice 136's risk export endpoint runs every
--      query under tenancy.ApplyTenant as atlas_app, identical to
--      slice 135.
--
--   Append-only ledger (canvas §4.3). The CHECK extension permits a
--      new READ-event action value in me_audit_log, which remains
--      append-only via its SELECT-+-INSERT-only RLS policy split.
--
-- Idempotency: ALTER TABLE ... DROP/ADD CONSTRAINT both succeed on
-- re-apply against an already-migrated database. Reversible via
-- 20260519000010_risk_export_meta_audit.down.sql.

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
        'risk_export'
    ));
