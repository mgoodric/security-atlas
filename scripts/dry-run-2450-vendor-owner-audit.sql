-- security-atlas #2450 dry run: the vendor owner changes #2129 wrote to
-- me_audit_log ('vendor_owner_update'), ahead of copying them into
-- vendor_audit_log, where new owner changes now go.
--
-- Read-only. Every statement runs in a READ ONLY transaction that is
-- rolled back. It prints counts per tenant and never a vendor id, a user id,
-- an owner's name or any before/after content. Run it as a role that can see
-- every tenant (the migrate/owner role, which bypasses RLS):
--
--   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/dry-run-2450-vendor-owner-audit.sql
--
-- Proposed move (a separate step, applied only with Matt's approval):
--
--   INSERT INTO vendor_audit_log
--       (tenant_id, vendor_id, actor, action_type, before_state, after_state, created_at)
--   SELECT m.tenant_id,
--          (m.after ->> 'vendor_id')::uuid,
--          COALESCE(m.actor_principal,
--                   NULLIF(m.user_id::text, '00000000-0000-0000-0000-000000000000'),
--                   'unknown:me_audit_log'),
--          'owner_changed', m.before, m.after, m.occurred_at
--     FROM me_audit_log m
--    WHERE m.action = 'vendor_owner_update'
--      AND NOT EXISTS (SELECT 1 FROM vendor_audit_log v
--                       WHERE v.tenant_id = m.tenant_id
--                         AND v.vendor_id = (m.after ->> 'vendor_id')::uuid
--                         AND v.created_at = m.occurred_at);
--
-- It copies; it does not delete. me_audit_log is append-only, so removing
-- the originals means lifting its trigger, which is its own decision. A row
-- whose actor was not a user (uuid.Nil, no actor_principal) is copied as
-- 'unknown:me_audit_log': the original never said who it was.
-- Nothing here changes a row.

BEGIN TRANSACTION READ ONLY;

-- 1. Per tenant: how many owner changes sit in me_audit_log, how many name
--    no person (uuid.Nil user_id), how many of those have no
--    actor_principal either (their actor is unknown), and how many name a
--    vendor that no longer exists.
SELECT m.tenant_id,
       count(*) AS owner_changes,
       count(*) FILTER (WHERE m.user_id = '00000000-0000-0000-0000-000000000000') AS nil_user,
       count(*) FILTER (WHERE m.user_id = '00000000-0000-0000-0000-000000000000'
                          AND m.actor_principal IS NULL) AS unknown_actor,
       count(*) FILTER (WHERE NOT EXISTS (
           SELECT 1 FROM vendors v
            WHERE v.tenant_id = m.tenant_id
              AND v.id::text = m.after ->> 'vendor_id')) AS vendor_gone,
       min(m.occurred_at)::date AS first,
       max(m.occurred_at)::date AS last
FROM me_audit_log m
WHERE m.action = 'vendor_owner_update'
GROUP BY m.tenant_id
ORDER BY owner_changes DESC, m.tenant_id;

-- 2. Rows the proposed move could not copy: no vendor_id in the after
--    value, or one that is not a uuid.
SELECT count(*) AS uncopyable
FROM me_audit_log m
WHERE m.action = 'vendor_owner_update'
  AND ((m.after ->> 'vendor_id') IS NULL
       OR (m.after ->> 'vendor_id') !~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$');

-- 3. vendor_audit_log rows already present, per tenant, so a re-run after
--    the move shows what landed.
SELECT tenant_id, count(*) AS vendor_audit_rows
FROM vendor_audit_log
GROUP BY tenant_id
ORDER BY vendor_audit_rows DESC, tenant_id;

ROLLBACK;
