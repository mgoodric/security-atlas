-- security-atlas #2401 dry run: which active users lose access once a JWT
-- session with an empty role claim derives nothing.
--
-- Read-only and counts-only. Every statement runs in a READ ONLY
-- transaction that is rolled back. It prints no user id, tenant id, name or
-- email: tenants are numbered 1..N in tenant_id order, which is stable
-- between runs but names nothing. Run it as a role that can see every
-- tenant (the migrate/owner role, which bypasses RLS):
--
--   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/dry-run-2401-roleless-users.sql
--
-- A JWT's role claim is the user's user_roles rows in that tenant at mint
-- time (internal/api/oauth/user_resolver.go), so a user with no row in the
-- tenant gets an empty claim. authz.BuildInput derived, per JWT session:
--   before = admin         if the user is a super_admin
--            grc_engineer  if the claim is empty
--   after  = admin         if the user is a super_admin
--            nothing       if the claim is empty: the session holds only
--                          its live user_roles rows, here none, so every
--                          tenant route answers 403.
-- oauth_client tokens and API keys are unchanged.

BEGIN TRANSACTION READ ONLY;

-- 1. Per tenant: active users with zero user_roles rows in that tenant.
--    users_losing_access excludes super_admins, who keep admin. A user
--    whose tenant has no tenants row shows under a NULL tenant_n.
WITH numbered AS (
    SELECT id, row_number() OVER (ORDER BY id) AS tenant_n
    FROM tenants
),
roleless AS (
    SELECT u.tenant_id, u.id
    FROM users u
    WHERE u.status = 'active'
      AND NOT EXISTS (
            SELECT 1 FROM user_roles ur
            WHERE ur.tenant_id = u.tenant_id AND ur.user_id = u.id::text)
)
SELECT n.tenant_n,
       count(*) AS active_users_without_roles,
       count(*) FILTER (WHERE sa.user_id IS NULL) AS users_losing_access,
       count(*) FILTER (WHERE sa.user_id IS NOT NULL) AS super_admins_unchanged
FROM roleless r
LEFT JOIN numbered n ON n.id = r.tenant_id
LEFT JOIN super_admins sa ON sa.user_id = r.id
GROUP BY n.tenant_n
ORDER BY n.tenant_n NULLS LAST;

-- 2. Totals, so the per-tenant rows can be checked against the whole
--    install: active users, and how many of them have no role anywhere.
SELECT count(*) AS active_users,
       count(*) FILTER (WHERE NOT EXISTS (
           SELECT 1 FROM user_roles ur
           WHERE ur.tenant_id = u.tenant_id AND ur.user_id = u.id::text)) AS active_users_without_roles
FROM users u
WHERE u.status = 'active';

-- 3. (#2494) Per tenant: role-less active memberships that are not the
--    user's home row. An assign into another tenant (POST
--    /v1/admin/users/assign) adds a users row there sharing the identity's
--    (idp_issuer, idp_subject); revoking its roles while keeping the
--    membership leaves that row role-less. Such a user signs in to their
--    home tenant as before and sees the no-role page only after switching
--    here. The home row is the one a local user's synthetic subject names
--    (idp_issuer 'urn:atlas:local', idp_subject = its id), otherwise the
--    identity's earliest row. Rows with an empty IdP tuple are their own
--    home. Super-admins are left out, as in 1.
WITH numbered AS (
    SELECT id, row_number() OVER (ORDER BY id) AS tenant_n
    FROM tenants
),
homes AS (
    SELECT DISTINCT ON (u.idp_issuer, u.idp_subject)
           u.idp_issuer, u.idp_subject, u.id AS home_id
    FROM users u
    WHERE u.idp_issuer <> '' OR u.idp_subject <> ''
    ORDER BY u.idp_issuer, u.idp_subject,
             (u.idp_issuer = 'urn:atlas:local' AND u.id::text = u.idp_subject) DESC,
             u.created_at, u.id
)
SELECT n.tenant_n,
       count(*) AS roleless_non_home_memberships
FROM users u
JOIN homes h ON h.idp_issuer = u.idp_issuer AND h.idp_subject = u.idp_subject
LEFT JOIN numbered n ON n.id = u.tenant_id
WHERE u.status = 'active'
  AND u.id <> h.home_id
  AND NOT EXISTS (
        SELECT 1 FROM user_roles ur
        WHERE ur.tenant_id = u.tenant_id AND ur.user_id = u.id::text)
  AND NOT EXISTS (
        SELECT 1 FROM super_admins sa WHERE sa.user_id = u.id)
GROUP BY n.tenant_n
ORDER BY n.tenant_n NULLS LAST;

ROLLBACK;
