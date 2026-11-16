-- security-atlas #2402 dry run: which API keys stop deriving control_owner
-- once an owner_roles label naming only viewer and/or auditor no longer
-- does.
--
-- Read-only. Every statement runs in a READ ONLY transaction that is
-- rolled back. It prints counts per tenant, never a key id, a user id or an
-- email. Run it as a role that can see every tenant (the migrate/owner
-- role, which bypasses RLS):
--
--   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/dry-run-2402-key-roles.sql
--
-- authz.BuildInput derives a key's credential role from its row:
--   before = admin          if is_admin
--            grc_engineer   if is_approver
--            control_owner  if owner_roles is non-empty
--            grc_engineer   otherwise
--   after  = unchanged, except a key whose owner_roles names only viewer
--            and/or auditor (and is neither admin nor approver):
--              unbound (issued_by NULL): exactly its label's roles
--              bound   (issued_by set):  nothing; the owner's live
--                                        user_roles rows alone
-- Effective roles in both cases also merge the live user_roles rows of the
-- key's principal (the owner's uuid for a bound key). A live key is one
-- not revoked, not expired and not past its rotation grace.

BEGIN TRANSACTION READ ONLY;

-- 1. Per tenant: live viewer/auditor-only keys whose derived role changes,
--    by binding and label. Every one of them loses evidence, artifact and
--    walkthrough writes unless its owner's live roles still grant them
--    (see 2).
WITH live AS (
    SELECT k.*
    FROM api_keys k
    WHERE k.revoked_at IS NULL
      AND (k.expires_at IS NULL OR k.expires_at > now())
      AND (k.retires_at IS NULL OR k.retires_at > now())
)
SELECT tenant_id,
       CASE WHEN issued_by IS NULL THEN 'unbound' ELSE 'bound' END AS binding,
       array_to_string(ARRAY(SELECT DISTINCT r FROM unnest(owner_roles) AS r ORDER BY r), ',') AS label,
       count(*) AS keys_changing
FROM live
WHERE NOT is_admin AND NOT is_approver
  AND cardinality(owner_roles) > 0
  AND owner_roles <@ ARRAY['auditor', 'viewer']::text[]
GROUP BY 1, 2, 3
ORDER BY 1, 2, 3;

-- 2. Per tenant: the bound keys from 1, by what the owner's live role set
--    leaves them. 'none' keys lose every grant (the owner holds no role or
--    is not active); 'read-only' keys lose control_owner's writes;
--    'writes' keys lose nothing they use, because the owner's roles
--    already cover control_owner.
WITH live AS (
    SELECT k.*
    FROM api_keys k
    WHERE k.revoked_at IS NULL
      AND (k.expires_at IS NULL OR k.expires_at > now())
      AND (k.retires_at IS NULL OR k.retires_at > now())
      AND NOT k.is_admin AND NOT k.is_approver
      AND k.issued_by IS NOT NULL
      AND cardinality(k.owner_roles) > 0
      AND k.owner_roles <@ ARRAY['auditor', 'viewer']::text[]
), owners AS (
    SELECT l.tenant_id, l.id,
           array_agg(ur.role) FILTER (WHERE ur.role IS NOT NULL AND u.status = 'active') AS roles
    FROM live l
    LEFT JOIN users u ON u.tenant_id = l.tenant_id AND u.id = l.issued_by
    LEFT JOIN user_roles ur ON ur.tenant_id = l.tenant_id AND ur.user_id = l.issued_by::text
    GROUP BY l.tenant_id, l.id
)
SELECT tenant_id,
       CASE
           WHEN roles IS NULL THEN 'none'
           WHEN roles && ARRAY['admin', 'grc_engineer', 'control_owner']::text[] THEN 'writes'
           ELSE 'read-only'
       END AS owner_leaves,
       count(*) AS bound_keys
FROM owners
GROUP BY 1, 2
ORDER BY 1, 2;

-- 3. Per tenant: live keys whose derivation does NOT change, so the two
--    counts add up to every live key.
SELECT tenant_id, count(*) AS keys_unchanged
FROM api_keys
WHERE revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > now())
  AND (retires_at IS NULL OR retires_at > now())
  AND (is_admin OR is_approver
       OR cardinality(owner_roles) = 0
       OR NOT (owner_roles <@ ARRAY['auditor', 'viewer']::text[]))
GROUP BY 1
ORDER BY 1;

ROLLBACK;
