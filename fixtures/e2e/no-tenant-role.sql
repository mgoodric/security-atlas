-- #2494 — Playwright e2e seed for `web/e2e/no-tenant-role.spec.ts`.
--
-- A tenant of the spec's own with one active member and no user_roles
-- row for them. The spec grants a role through the admin API, so every
-- run clears the member's roles first.
--
-- Tenant UUID matches TENANT in web/e2e/no-tenant-role.spec.ts; the `2494`
-- suffix carries the ticket.

\set ON_ERROR_STOP on

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-000000002494', 'No role (e2e)', false)
ON CONFLICT DO NOTHING;

-- A second tenant the member's token also lists, so the page offers the
-- tenant switcher.
INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-000000002495', 'No role other (e2e)', false)
ON CONFLICT DO NOTHING;

BEGIN;
SET LOCAL app.current_tenant = '00000000-0000-0000-0000-000000002494';
INSERT INTO users (id, tenant_id, email, display_name, status, idp_issuer, idp_subject)
VALUES (
    '44444444-4444-4444-4444-444444442494',
    '00000000-0000-0000-0000-000000002494',
    'no-tenant-role-member-e2e@example.invalid',
    'No Tenant Role Member E2E',
    'active', '', ''
)
ON CONFLICT (id) DO UPDATE SET status = 'active';
DELETE FROM user_roles
WHERE tenant_id = '00000000-0000-0000-0000-000000002494'
  AND user_id = '44444444-4444-4444-4444-444444442494';
COMMIT;
