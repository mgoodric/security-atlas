-- security-atlas — slice 139: audit-periods + vendors data export.
--
-- One schema change: extend `me_audit_log.action` CHECK constraint to
-- permit two new values:
--
--   audit_periods_export — bulk download (csv / json / xlsx) of the
--                          audit_periods register, including frozen
--                          metadata columns (frozen_at, frozen_by,
--                          frozen_hash). Does NOT include the cosigned
--                          bundle bytes — slice 030 owns that surface.
--
--   vendors_export        — bulk download (csv / json / xlsx) of the
--                           vendor register. Vendor email is masked at
--                           v1 (`*@domain.tld`); un-masked column
--                           deferred to v3 column selection.
--
-- Both are READ-event meta-audits, identical in shape to slice 135's
-- `audit_log_export`. The CHECK extension is the load-bearing schema
-- change: AC-5 / AC-6 require a me_audit_log row on every export
-- attempt, and the existing CHECK from slice 135 only allowed five
-- values. This migration adds two more.
--
-- Constitutional invariants honored:
--
--   #6 Tenant isolation via PostgreSQL RLS. The CHECK extension does
--      not alter RLS; the slice-139 export endpoints run every query
--      under tenancy.ApplyTenant as atlas_app, identical to slice 135.
--
--   Append-only ledger (canvas §4.3). The CHECK extension permits two
--      new READ-event action values in me_audit_log, which remains
--      append-only via its SELECT-+-INSERT-only RLS policy split.
--
--   #10 Audit-period freezing. The audit_periods_export row set INCLUDES
--       frozen_at / frozen_by / frozen_hash so the freeze trail is
--       legible offline (canvas §8.4). Frozen-row content matches the
--       live row at export time; no point-in-time replay is performed
--       on the export path (forensics consumers replay against the
--       source `audit_periods` table directly).
--
-- Idempotency: ALTER TABLE ... DROP/ADD CONSTRAINT both succeed on
-- re-apply against an already-migrated database. Reversible via
-- 20260519000000_audit_periods_vendors_export.down.sql.

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
        'vendors_export'
    ));
