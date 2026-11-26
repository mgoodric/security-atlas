-- #2503 — Playwright e2e seed for `web/e2e/tenant-admin-users.spec.ts`.
--
-- A tenant of the spec's own with two active members: a tenant admin
-- (a user_roles 'admin' row, no super_admin) and a viewer. The spec signs
-- in as the admin and grants the viewer a role, so each run first resets
-- the viewer's roles to just 'viewer'. The access audit log is
-- append-only and keeps every run's rows.
--
-- Tenant UUID matches TENANT in web/e2e/tenant-admin-users.spec.ts; the
-- `2503` suffix carries the ticket.

\set ON_ERROR_STOP on

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-000000002503', 'Tenant admin users (e2e)', false)
ON CONFLICT DO NOTHING;

BEGIN;
SET LOCAL app.current_tenant = '00000000-0000-0000-0000-000000002503';
INSERT INTO users (id, tenant_id, email, display_name, status, idp_issuer, idp_subject)
VALUES
    ('44444444-4444-4444-4444-444444442503',
     '00000000-0000-0000-0000-000000002503',
     'tenant-admin-e2e@example.invalid',
     'Tenant Admin E2E',
     'active', '', ''),
    ('44444444-4444-4444-4444-444444442513',
     '00000000-0000-0000-0000-000000002503',
     'tenant-member-e2e@example.invalid',
     'Tenant Member E2E',
     'active', '', '')
ON CONFLICT (id) DO UPDATE SET status = 'active';
DELETE FROM user_roles
WHERE tenant_id = '00000000-0000-0000-0000-000000002503';
INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES
    ('00000000-0000-0000-0000-000000002503', '44444444-4444-4444-4444-444444442503', 'admin', 'e2e:tenant-admin-users'),
    ('00000000-0000-0000-0000-000000002503', '44444444-4444-4444-4444-444444442513', 'viewer', 'e2e:tenant-admin-users');
COMMIT;
