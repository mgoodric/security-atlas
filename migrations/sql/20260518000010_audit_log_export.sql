-- security-atlas — slice 135: data-export library + audit-log export
-- endpoint.
--
-- One schema change: extend `me_audit_log.action` CHECK constraint to
-- permit the new `audit_log_export` value. AC-9 of slice 135 records
-- EVERY export attempt (success, 403, 413, 500) as a `me_audit_log`
-- row; the existing CHECK from slice 124 only allowed
-- ('profile.update', 'preferences.update', 'session.revoke',
-- 'audit_log_query_unified') so this extension is load-bearing.
--
-- `audit_log_export` is distinct from `audit_log_query_unified`
-- (slice 124's read meta-audit action). Both are append-only READ
-- events meta-audited into me_audit_log to keep the wire shape uniform,
-- but they describe different operations:
--
--   audit_log_query_unified — paginated screen-read of the unified log
--   audit_log_export        — bulk download (csv / json / xlsx)
--
-- Two distinct actions enable downstream consumers (forensic queries,
-- threat-detection rules) to distinguish "operator browsed the log"
-- from "operator dumped the log" — which carry materially different
-- threat-model weight (the latter being a bulk-PII-extraction event).
--
-- Constitutional invariants honored:
--
--   #6 Tenant isolation via PostgreSQL RLS. The CHECK extension does
--      not alter RLS; the slice-135 export endpoint runs every query
--      under tenancy.ApplyTenant as atlas_app, identical to slice 124.
--
--   Append-only ledger (canvas §4.3). The CHECK extension permits a
--      new READ-event action value in me_audit_log, which remains
--      append-only via its SELECT-+-INSERT-only RLS policy split.
--
-- Idempotency: ALTER TABLE ... DROP/ADD CONSTRAINT both succeed on
-- re-apply against an already-migrated database. Reversible via
-- 20260518000010_audit_log_export.down.sql.

ALTER TABLE me_audit_log
    DROP CONSTRAINT IF EXISTS me_audit_log_action_check;

ALTER TABLE me_audit_log
    ADD CONSTRAINT me_audit_log_action_check
    CHECK (action IN (
        'profile.update',
        'preferences.update',
        'session.revoke',
        'audit_log_query_unified',
        'audit_log_export'
    ));
