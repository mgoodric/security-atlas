-- security-atlas #2381 dry run: which JWT sessions stop deriving
-- control_owner once a viewer/auditor role claim derives no credential role.
--
-- Read-only. Every statement runs in a READ ONLY transaction that is
-- rolled back. It prints counts per tenant and role set, never a user id or
-- an email. Run it as a role that can see every tenant (the migrate/owner
-- role, which bypasses RLS):
--
--   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/dry-run-2381-roles.sql
--
-- A JWT's role claim is the user's user_roles rows in that tenant at mint
-- time (internal/api/oauth/user_resolver.go). jwtmw copies it into
-- OwnerRoles, and authz.BuildInput derived from it, per JWT session:
--   before = admin          if the user is a super_admin
--            control_owner  if the claim is non-empty
--            grc_engineer   if the claim is empty
--   after  = unchanged, except a claim naming only viewer and/or auditor
--            derives nothing: the session holds its live user_roles rows
--            and loses every control_owner-only grant (evidence, artifact
--            and walkthrough writes; evidence, artifact, walkthrough and
--            framework-scope reads its own role does not give).
-- Effective roles in both cases also merge the live user_roles rows (#2239).
-- API keys and oauth_client tokens are unchanged.

BEGIN TRANSACTION READ ONLY;

-- 1. Per tenant and role set: active JWT users whose effective role set
--    changes. roles_before / roles_after are the effective sets.
WITH grants AS (
    SELECT ur.tenant_id, u.id AS user_id,
           array_agg(DISTINCT ur.role ORDER BY ur.role) AS assigned
    FROM user_roles ur
    JOIN users u ON u.tenant_id = ur.tenant_id AND u.id::text = ur.user_id
    WHERE u.status = 'active'
    GROUP BY ur.tenant_id, u.id
)
SELECT g.tenant_id,
       array_to_string(ARRAY(SELECT DISTINCT r FROM unnest(g.assigned || ARRAY['control_owner']) AS r ORDER BY r), ',') AS roles_before,
       array_to_string(g.assigned, ',') AS roles_after,
       count(*) AS users_losing_control_owner
FROM grants g
LEFT JOIN super_admins sa ON sa.user_id = g.user_id
WHERE sa.user_id IS NULL
  AND g.assigned <@ ARRAY['auditor', 'viewer']::text[]
GROUP BY 1, 2, 3
ORDER BY 1, 2, 3;

-- 2. Per tenant: active JWT users whose effective roles do NOT change
--    (any of admin / grc_engineer / control_owner, or a super_admin).
--    Shown so the two counts add up to the active users with roles.
WITH grants AS (
    SELECT ur.tenant_id, u.id AS user_id,
           array_agg(DISTINCT ur.role) AS assigned
    FROM user_roles ur
    JOIN users u ON u.tenant_id = ur.tenant_id AND u.id::text = ur.user_id
    WHERE u.status = 'active'
    GROUP BY ur.tenant_id, u.id
)
SELECT g.tenant_id, count(*) AS users_unchanged
FROM grants g
LEFT JOIN super_admins sa ON sa.user_id = g.user_id
WHERE sa.user_id IS NOT NULL
   OR NOT (g.assigned <@ ARRAY['auditor', 'viewer']::text[])
GROUP BY 1
ORDER BY 1;

-- 3. Viewer/auditor-only users assigned as a control's owner
--    (control_owner_assignments): they lose evidence, artifact and
--    walkthrough writes for that control. Counts only. A non-zero count
--    names users who need a control_owner grant before rollout.
SELECT u.tenant_id, count(DISTINCT u.id) AS readonly_users_assigned_to_controls
FROM users u
JOIN user_roles ur ON ur.tenant_id = u.tenant_id AND ur.user_id = u.id::text
WHERE u.status = 'active'
  AND NOT EXISTS (
        SELECT 1 FROM user_roles x
        WHERE x.tenant_id = u.tenant_id AND x.user_id = u.id::text
          AND x.role NOT IN ('auditor', 'viewer'))
  AND EXISTS (SELECT 1 FROM control_owner_assignments a WHERE a.tenant_id = u.tenant_id AND a.owner_user_id = u.id)
GROUP BY 1
ORDER BY 1;

ROLLBACK;
