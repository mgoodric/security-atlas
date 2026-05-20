-- security-atlas — slice 138: ledger entities data export
-- (evidence + policies + exceptions + samples).
--
-- One schema change: extend `me_audit_log.action` CHECK constraint to
-- permit FOUR new values:
--   * `evidence_export`
--   * `policies_export`
--   * `exceptions_export`
--   * `samples_export`
--
-- Slice 138 records EVERY ledger-entity-export attempt (success, 400,
-- 403, 413, 429, 500) as a `me_audit_log` row, mirroring the slice
-- 135 / 136 / 137 / 139 pattern. Each entity gets its own action
-- value so a forensic query like
--   WHERE action IN ('evidence_export', 'policies_export', ...)
-- cleanly enumerates ledger-entity extraction events. Different
-- entity, different downstream consumer (evidence exports feed
-- auditor handoff, policies feed acknowledgement spot-check, etc.),
-- so distinct labels are correct.
--
-- After slice 137 merged, the constraint permits
--   ('profile.update', 'preferences.update', 'session.revoke',
--    'audit_log_query_unified', 'audit_log_export',
--    'audit_periods_export', 'vendors_export', 'risk_export',
--    'controls_export')
-- so this extension adds exactly four new values.
--
-- Plural-of-entity convention (slice 138 D-locked): `evidence_export`,
-- `policies_export`, `exceptions_export`, `samples_export`. Matches
-- slice 137 (`controls_export`) and slice 139 (`audit_periods_export`,
-- `vendors_export`). Slice 136's singular `risk_export` (one register)
-- remains the outlier; the slice 138 entities each support many rows
-- per tenant so plural is correct.
--
-- Constitutional invariants honored:
--
--   #6 Tenant isolation via PostgreSQL RLS. The CHECK extension does
--      not alter RLS; slice 138's per-entity export endpoints run
--      every query under tenancy.ApplyTenant as atlas_app, identical
--      to slices 135 / 136 / 137 / 139.
--
--   Append-only ledger (canvas §4.3). The CHECK extension permits
--      four new READ-event action values in me_audit_log, which
--      remains append-only via its SELECT-+-INSERT-only RLS policy
--      split.
--
-- Idempotency: ALTER TABLE ... DROP/ADD CONSTRAINT both succeed on
-- re-apply against an already-migrated database. Reversible via
-- 20260520000010_ledger_entities_export_meta_audit.down.sql.

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
        'samples_export'
    ));
