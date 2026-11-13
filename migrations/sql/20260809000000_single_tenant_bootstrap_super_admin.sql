-- security-atlas — single-tenant install: the bootstrap admin is super-admin
-- (security-atlas#1759, spec #1758).
--
-- BACKGROUND:
--
--   On a single-tenant install the bootstrap admin must hold super_admin, so
--   that admin endpoints gated on super_admin (global catalog writes, #1757)
--   don't lock the only operator out. New installs get the grant at
--   bootstrap: deploy/docker/bootstrap/seed.sql and the OIDC first-install
--   path (users.Store.BootstrapFirstInstallOrUpsert) both write it. Installs
--   that predate those writers, or whose grant was lost, may lack it. This
--   migration grants it once on upgrade.
--
-- WHAT IT DOES:
--
--   1. Extends super_admins.granted_via CHECK (superset) with
--      'single_tenant_upgrade', so the provenance of this grant is
--      distinguishable from a first-install or manual grant.
--   2. When exactly one tenant exists (the all-zeros tenant is not a
--      tenant), grants super_admin to that tenant's bootstrap admin — the
--      oldest active user holding role='admin' there — if they lack it.
--
--   With zero tenants, or two or more, step 2 inserts nothing: on a
--   multi-tenant install super_admin stays a deliberate, manual grant.
--
-- IDEMPOTENCY / REVERSIBILITY:
--
--   ON CONFLICT (user_id) DO NOTHING makes step 2 a no-op when the grant
--   exists; re-applying the file is safe. The down migration removes the
--   rows this migration wrote and restores the two-value CHECK.

-- ===== 1. super_admins.granted_via CHECK extension =====

ALTER TABLE super_admins
    DROP CONSTRAINT IF EXISTS super_admins_granted_via_chk;

ALTER TABLE super_admins
    ADD CONSTRAINT super_admins_granted_via_chk
    CHECK (granted_via IN ('bootstrap_first_install', 'manual_grant', 'single_tenant_upgrade'));

-- ===== 2. Grant the single tenant's bootstrap admin super_admin =====

INSERT INTO super_admins (user_id, granted_via)
SELECT u.id, 'single_tenant_upgrade'
FROM users u
JOIN user_roles ur
  ON ur.tenant_id = u.tenant_id
 AND ur.user_id = u.id::text
 AND ur.role = 'admin'
WHERE u.status = 'active'
  AND u.tenant_id <> '00000000-0000-0000-0000-000000000000'::uuid
  AND (SELECT COUNT(*) FROM tenants
        WHERE id <> '00000000-0000-0000-0000-000000000000'::uuid) = 1
  AND u.tenant_id = (SELECT id FROM tenants
                      WHERE id <> '00000000-0000-0000-0000-000000000000'::uuid)
ORDER BY u.created_at ASC, u.id ASC
LIMIT 1
ON CONFLICT (user_id) DO NOTHING;
