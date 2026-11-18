-- security-atlas #2431 dry run: which API keys bound to a user lose roles
-- once their owner_roles label caps the owner's live roles.
--
-- Read-only. Every statement runs in a READ ONLY transaction that is
-- rolled back. It prints counts per tenant, never a key id, a user id or an
-- email. Run it as a role that can see every tenant (the migrate/owner
-- role, which bypasses RLS):
--
--   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/dry-run-2431-bound-key-ceiling.sql
--
-- A key is in scope when it is live (not revoked, not expired, not past its
-- rotation grace), bound to a user (issued_by set), labelled (owner_roles
-- non-empty) and carries neither the is_admin nor the is_approver flag.
-- For such a key:
--   before = (control_owner unless the label names only viewer/auditor)
--            UNION the owner's live user_roles
--   ceiling = each label entry that is a canonical role, plus control_owner
--             for any free-text entry
--   after  = ceiling                                if the owner is live admin
--            ceiling INTERSECT owner's live roles   otherwise
-- The owner's live roles are its user_roles rows, and none when its users
-- row is not 'active' (the resolver's rule, #2239).
--
-- The script compares role sets, not route decisions:
--   'none'      the key keeps no role and is refused everywhere;
--   'narrowed'  the key keeps some roles but fewer than before;
--   'unchanged' the role set is the same.
-- TestDecide2431_BoundKeyRoleMatrix shows a smaller role set never gains a
-- route.

BEGIN TRANSACTION READ ONLY;

-- 1. Per tenant: in-scope keys by owner_roles shape and what the cap leaves them.
WITH bound AS (
    SELECT k.tenant_id, k.id, k.issued_by, k.owner_roles
    FROM api_keys k
    WHERE k.revoked_at IS NULL
      AND (k.expires_at IS NULL OR k.expires_at > now())
      AND (k.retires_at IS NULL OR k.retires_at > now())
      AND NOT k.is_admin AND NOT k.is_approver
      AND k.issued_by IS NOT NULL
      AND cardinality(k.owner_roles) > 0
), owned AS (
    SELECT b.tenant_id, b.id, b.owner_roles,
           ARRAY(SELECT DISTINCT CASE
                     WHEN r IN ('admin', 'grc_engineer', 'control_owner', 'auditor', 'viewer') THEN r
                     ELSE 'control_owner'
                 END
                 FROM unnest(b.owner_roles) AS r) AS ceiling,
           COALESCE(array_agg(ur.role) FILTER (
               WHERE ur.role IS NOT NULL AND COALESCE(u.status, 'active') = 'active'
           ), '{}') AS live_roles
    FROM bound b
    LEFT JOIN users u ON u.tenant_id = b.tenant_id AND u.id = b.issued_by
    LEFT JOIN user_roles ur ON ur.tenant_id = b.tenant_id AND ur.user_id = b.issued_by::text
    GROUP BY b.tenant_id, b.id, b.owner_roles
), sets AS (
    SELECT tenant_id,
           CASE
               WHEN owner_roles <@ ARRAY['admin', 'grc_engineer', 'control_owner', 'auditor', 'viewer']::text[] THEN 'canonical'
               WHEN owner_roles && ARRAY['admin', 'grc_engineer', 'control_owner', 'auditor', 'viewer']::text[] THEN 'mixed'
               ELSE 'free-text'
           END AS owner_roles_shape,
           ARRAY(SELECT unnest(
                     CASE WHEN owner_roles <@ ARRAY['auditor', 'viewer']::text[]
                          THEN '{}'::text[] ELSE ARRAY['control_owner'] END)
                 UNION SELECT unnest(live_roles)) AS before,
           CASE WHEN 'admin' = ANY (live_roles) THEN ceiling
                ELSE ARRAY(SELECT unnest(ceiling) INTERSECT SELECT unnest(live_roles))
           END AS after
    FROM owned
)
SELECT tenant_id, owner_roles_shape,
       CASE
           WHEN cardinality(after) = 0 THEN 'none'
           WHEN after @> before AND before @> after THEN 'unchanged'
           ELSE 'narrowed'
       END AS outcome,
       count(*) AS bound_keys
FROM sets
GROUP BY 1, 2, 3
ORDER BY 1, 2, 3;

-- 2. Per tenant: live keys out of scope, which do not change: unbound,
--    unlabelled (no cap, Matt 2026-11-18), or admin/approver-flagged.
SELECT tenant_id, count(*) AS keys_out_of_scope
FROM api_keys
WHERE revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > now())
  AND (retires_at IS NULL OR retires_at > now())
  AND (is_admin OR is_approver OR issued_by IS NULL OR cardinality(owner_roles) = 0)
GROUP BY 1
ORDER BY 1;

ROLLBACK;
