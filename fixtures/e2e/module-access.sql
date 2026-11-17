-- #2172 — Playwright e2e seed for `web/e2e/module-access.spec.ts`.
--
-- A tenant of the spec's own with one active grc_engineer member. The
-- member's module access is cleared on each run, so the spec starts from
-- "all modules"; the access audit log is append-only and keeps every
-- run's rows. No feature_flags rows: every module reads its seed default
-- (every optional module on except Board reporting).
--
-- Tenant UUID matches TENANT in web/e2e/module-access.spec.ts; the `2172`
-- suffix carries the ticket.

\set ON_ERROR_STOP on

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-000000002172', 'Module access (e2e)', false)
ON CONFLICT DO NOTHING;

BEGIN;
SET LOCAL app.current_tenant = '00000000-0000-0000-0000-000000002172';
INSERT INTO users (id, tenant_id, email, display_name, status, idp_issuer, idp_subject)
VALUES (
    '44444444-4444-4444-4444-444444442172',
    '00000000-0000-0000-0000-000000002172',
    'module-access-member-e2e@example.invalid',
    'Module Access Member E2E',
    'active', '', ''
)
ON CONFLICT (id) DO UPDATE SET status = 'active';
INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES ('00000000-0000-0000-0000-000000002172', '44444444-4444-4444-4444-444444442172', 'grc_engineer', 'e2e:module-access')
ON CONFLICT DO NOTHING;
DELETE FROM member_module_access
WHERE tenant_id = '00000000-0000-0000-0000-000000002172';
COMMIT;

-- The spec switches Exceptions off and back on; pin it on in case a run
-- stopped between the two.
INSERT INTO feature_flags (tenant_id, flag_key, enabled, description, category, last_changed_by, last_changed_at)
VALUES ('00000000-0000-0000-0000-000000002172', 'exceptions.enabled', true,
        'Exceptions module. On for the #2172 module-access spec tenant.', 'controls', 'e2e-fixture', now())
ON CONFLICT (tenant_id, flag_key) DO UPDATE SET enabled = true;
