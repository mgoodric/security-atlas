-- security-atlas #2239 dry run: whose effective roles change once
-- Engine.Decide reads user_roles for "user:<uuid>" JWT subjects.
--
-- Read-only. Every statement runs in a READ ONLY transaction that is
-- rolled back. It prints counts per tenant and role set, never a user id or
-- an email. Run it as a role that can see every tenant (the migrate/owner
-- role, which bypasses RLS):
--
--   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/dry-run-2239-roles.sql
--
-- What changes, per JWT (password / OIDC / PKCE / device-grant) session:
--   derived role today   = admin          if the user is a super_admin
--                          control_owner  if they hold any user_roles row
--                                         (the JWT's role claim is non-empty)
--                          grc_engineer   otherwise
--   effective today      = the derived role only (the user_roles lookup misses)
--   effective after fix  = the derived role UNION their user_roles rows
-- A user with no user_roles row is unchanged. Bound and unbound API keys and
-- oauth_client tokens are unchanged: their lookup key is not rewritten.

BEGIN TRANSACTION READ ONLY;

-- 1. Role-set change per tenant, counts only. Rows where roles_before equals
--    roles_after are users whose effective access does not change.
WITH grants AS (
    SELECT ur.tenant_id, u.id AS user_id,
           array_agg(DISTINCT ur.role ORDER BY ur.role) AS assigned
    FROM user_roles ur
    JOIN users u ON u.tenant_id = ur.tenant_id AND u.id::text = ur.user_id
    WHERE u.status = 'active'
    GROUP BY ur.tenant_id, u.id
), sets AS (
    SELECT g.tenant_id,
           CASE WHEN sa.user_id IS NOT NULL
                THEN ARRAY['admin'] ELSE ARRAY['control_owner'] END AS before_roles,
           g.assigned
    FROM grants g
    LEFT JOIN super_admins sa ON sa.user_id = g.user_id
)
SELECT tenant_id,
       array_to_string(before_roles, ',') AS roles_before,
       array_to_string(ARRAY(SELECT DISTINCT r FROM unnest(before_roles || assigned) AS r ORDER BY r), ',') AS roles_after,
       count(*) AS users
FROM sets
GROUP BY 1, 2, 3
ORDER BY 1, 2, 3;

-- 2. Unified audit-log API (GET /v1/admin/audit-log/unified,
--    /v1/admin/audit-log/export, and the privileged view of
--    /v1/activity/unified): JWT users who gain access through an auditor or
--    grc_engineer row.
SELECT ur.tenant_id, count(DISTINCT u.id) AS users_gaining_unified_audit_log
FROM user_roles ur
JOIN users u ON u.tenant_id = ur.tenant_id AND u.id::text = ur.user_id
WHERE u.status = 'active' AND ur.role IN ('auditor', 'grc_engineer')
GROUP BY 1
ORDER BY 1;

-- 3. Rows that would STOP matching: a user_id stored with the "user:"
--    prefix. Role assignment writes the bare uuid, so this should be 0.
SELECT count(*) AS prefixed_rows_that_stop_matching
FROM user_roles
WHERE user_id LIKE 'user:%';

-- 4. Users who are not active but still hold user_roles rows (a SCIM
--    deactivation keeps them). The resolver now ignores these rows. Their JWT
--    sessions are unchanged (they never matched); an API key bound to such a
--    user drops to its credential-derived role.
SELECT ur.tenant_id, count(DISTINCT u.id) AS inactive_users_with_roles
FROM user_roles ur
JOIN users u ON u.tenant_id = ur.tenant_id AND u.id::text = ur.user_id
WHERE u.status <> 'active'
GROUP BY 1
ORDER BY 1;

ROLLBACK;
