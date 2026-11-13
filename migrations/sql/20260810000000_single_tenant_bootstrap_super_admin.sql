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
--   that predate those writers may have no super-admin at all. This
--   migration grants one on upgrade, only when it is missing.
--
-- WHAT IT DOES:
--
--   1. Extends super_admins.granted_via CHECK (superset) with
--      'single_tenant_upgrade', so the provenance of this grant is
--      distinguishable from a first-install or manual grant.
--   2. Grants super_admin to the bootstrap admin when ALL of these hold:
--        - no super_admins row exists at all. If one does, an operator has
--          a super-admin already (possibly moved to another user, or held
--          by a deactivated bootstrap admin); granting a second one would
--          create an unchosen grant;
--        - exactly one tenant exists (the all-zeros tenant is not a tenant);
--        - an active user holding role='admin' in that tenant exists.
--      The bootstrap admin is, in order of preference:
--        a. an identified bootstrap user: the Docker seed's well-known user
--           id (deploy/docker/bootstrap/seed.sql) or the user on a
--           'bootstrap_first_install' me_audit_log row;
--        b. otherwise the oldest active admin of the tenant.
--      A NOTICE names the granted user, or says why nothing was granted.
--
--   With zero tenants, or two or more, nothing is granted: on a
--   multi-tenant install super_admin stays a deliberate, manual grant.
--
-- IDEMPOTENCY / REVERSIBILITY:
--
--   After a grant, the "no super_admins row" condition is false, so
--   re-applying the file is a no-op. The down migration removes the rows
--   this migration wrote and restores the two-value CHECK.

-- ===== 1. super_admins.granted_via CHECK extension =====

ALTER TABLE super_admins
    DROP CONSTRAINT IF EXISTS super_admins_granted_via_chk;

ALTER TABLE super_admins
    ADD CONSTRAINT super_admins_granted_via_chk
    CHECK (granted_via IN ('bootstrap_first_install', 'manual_grant', 'single_tenant_upgrade'));

-- ===== 2. Grant the single tenant's bootstrap admin super_admin =====

DO $$
DECLARE
    nil_tenant   CONSTANT uuid := '00000000-0000-0000-0000-000000000000';
    -- deploy/docker/bootstrap/seed.sql's well-known default user id.
    seed_user    CONSTANT uuid := 'c0000000-0000-4000-8000-000000000001';
    tenant_count int;
    the_tenant   uuid;
    grantee      uuid;
BEGIN
    IF EXISTS (SELECT 1 FROM super_admins) THEN
        RAISE NOTICE 'single_tenant_upgrade: a super-admin already exists; nothing granted';
        RETURN;
    END IF;

    SELECT COUNT(*) INTO tenant_count FROM tenants WHERE id <> nil_tenant;
    IF tenant_count <> 1 THEN
        RAISE NOTICE 'single_tenant_upgrade: % tenants exist, not a single-tenant install; nothing granted',
            tenant_count;
        RETURN;
    END IF;
    SELECT id INTO the_tenant FROM tenants WHERE id <> nil_tenant;

    -- a. An identified bootstrap user who is still an active admin.
    SELECT u.id INTO grantee
    FROM users u
    JOIN user_roles ur
      ON ur.tenant_id = u.tenant_id AND ur.user_id = u.id::text AND ur.role = 'admin'
    WHERE u.tenant_id = the_tenant
      AND u.status = 'active'
      AND (u.id = seed_user
           OR u.id IN (SELECT m.user_id FROM me_audit_log m
                        WHERE m.action = 'bootstrap_first_install'
                          AND m.tenant_id = the_tenant))
    ORDER BY u.created_at ASC, u.id ASC
    LIMIT 1;

    -- b. Otherwise the oldest active admin.
    IF grantee IS NULL THEN
        SELECT u.id INTO grantee
        FROM users u
        JOIN user_roles ur
          ON ur.tenant_id = u.tenant_id AND ur.user_id = u.id::text AND ur.role = 'admin'
        WHERE u.tenant_id = the_tenant
          AND u.status = 'active'
        ORDER BY u.created_at ASC, u.id ASC
        LIMIT 1;
    END IF;

    IF grantee IS NULL THEN
        RAISE NOTICE 'single_tenant_upgrade: tenant % has no active admin; nothing granted', the_tenant;
        RETURN;
    END IF;

    INSERT INTO super_admins (user_id, granted_via)
    VALUES (grantee, 'single_tenant_upgrade')
    ON CONFLICT (user_id) DO NOTHING;
    RAISE NOTICE 'single_tenant_upgrade: granted super_admin to user % (tenant %)', grantee, the_tenant;
END
$$;
