-- Slice 082 / #2755 — Playwright e2e seed for
-- `web/e2e/admin-bootstrap.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). #2755 moved the spec off the demo tenant onto a tenant of
-- its own, 00000000-0000-0000-0000-2755a0000001, holding:
--
--   - an admin member (a user_roles 'admin' row) the end-to-end flow
--     signs in as, and a viewer member for the non-admin checks;
--   - no feature-flag overrides and no SSO config: the flow turns the
--     default-off `vendor.claims` flag on and saves an SSO config, so
--     each run deletes both first and starts from the same state.
--
-- The spec mints each member's JWT through /v1/test/issue-jwt.

\set ON_ERROR_STOP on

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-2755a0000001', 'Admin bootstrap (e2e)', false)
ON CONFLICT DO NOTHING;

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-2755a0000001';

INSERT INTO users (id, tenant_id, email, display_name, status, idp_issuer, idp_subject)
VALUES
    ('44444444-4444-4444-4444-2755a0000101',
     '00000000-0000-0000-0000-2755a0000001',
     'admin-bootstrap-admin-e2e@example.invalid',
     'Admin Bootstrap Admin E2E',
     'active', '', ''),
    ('44444444-4444-4444-4444-2755a0000102',
     '00000000-0000-0000-0000-2755a0000001',
     'admin-bootstrap-viewer-e2e@example.invalid',
     'Admin Bootstrap Viewer E2E',
     'active', '', '')
ON CONFLICT (id) DO UPDATE SET status = 'active';

DELETE FROM user_roles
WHERE tenant_id = '00000000-0000-0000-0000-2755a0000001';
INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES
    ('00000000-0000-0000-0000-2755a0000001', '44444444-4444-4444-4444-2755a0000101', 'admin', 'e2e:admin-bootstrap'),
    ('00000000-0000-0000-0000-2755a0000001', '44444444-4444-4444-4444-2755a0000102', 'viewer', 'e2e:admin-bootstrap');

-- ============================================================
-- Feature flag toggle target, reset each run
-- ============================================================
-- /admin/features lists the flag registry, not arbitrary feature_flags
-- rows, so the toggle target is a registered flag that defaults off:
-- `vendor.claims`. The flow turns it on, which writes this tenant's
-- override row; deleting the row puts the flag back on its default.
DELETE FROM feature_flags
WHERE tenant_id = '00000000-0000-0000-0000-2755a0000001';

-- The flow saves an SSO config; start each run without one.
DELETE FROM oidc_idp_configs
WHERE tenant_id = '00000000-0000-0000-0000-2755a0000001';

COMMIT;
