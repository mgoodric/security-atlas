-- #2381 — Playwright e2e seed for `web/e2e/feature-flag-gating.spec.ts`.
--
-- One active viewer user, holding the viewer role in user_roles, in each
-- of the spec's two tenants. A viewer JWT's role claim no longer derives a
-- role by itself (#2381): the session's roles are the user's live
-- user_roles rows, so the spec signs the viewer in as a user that has one,
-- the shape a real login mints. The tenants are the spec's own.

\set ON_ERROR_STOP on

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES
    ('00000000-0000-0000-0000-0000000020f0', 'Flags off (e2e)', false),
    ('00000000-0000-0000-0000-000000002170', 'Questionnaires off (e2e)', false)
ON CONFLICT DO NOTHING;

BEGIN;
SET LOCAL app.current_tenant = '00000000-0000-0000-0000-0000000020f0';
INSERT INTO users (id, tenant_id, email, display_name, status, idp_issuer, idp_subject)
VALUES (
    '44444444-4444-4444-4444-4444444420f0',
    '00000000-0000-0000-0000-0000000020f0',
    'flags-off-viewer-e2e@example.invalid',
    'Flags Off Viewer E2E',
    'active', '', ''
)
ON CONFLICT (id) DO UPDATE SET status = 'active';
INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES ('00000000-0000-0000-0000-0000000020f0', '44444444-4444-4444-4444-4444444420f0', 'viewer', 'e2e:feature-flag-gating')
ON CONFLICT DO NOTHING;
COMMIT;

BEGIN;
SET LOCAL app.current_tenant = '00000000-0000-0000-0000-000000002170';
INSERT INTO users (id, tenant_id, email, display_name, status, idp_issuer, idp_subject)
VALUES (
    '44444444-4444-4444-4444-444444442170',
    '00000000-0000-0000-0000-000000002170',
    'questionnaires-off-viewer-e2e@example.invalid',
    'Questionnaires Off Viewer E2E',
    'active', '', ''
)
ON CONFLICT (id) DO UPDATE SET status = 'active';
INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES ('00000000-0000-0000-0000-000000002170', '44444444-4444-4444-4444-444444442170', 'viewer', 'e2e:feature-flag-gating')
ON CONFLICT DO NOTHING;
COMMIT;
