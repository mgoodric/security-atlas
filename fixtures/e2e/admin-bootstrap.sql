-- Slice 082 — Playwright e2e seed for `web/e2e/admin-bootstrap.spec.ts`.
-- Extended by Slice 115 — FULL coverage (admin user, viewer user, IdP connection, invited member, RBAC roles).
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The admin-bootstrap spec's preconditions (per its preamble):
--
--   - TEST_ADMIN_BEARER carries an admin credential with admin role
--   - TEST_VIEWER_BEARER carries a non-admin credential with viewer role
--   - the platform was seeded with at least one feature flag
--   - the platform has at least one OIDC IdP connection configured
--   - the platform has an invited (disabled) member account
--   - the platform has at least one RBAC role assignment
--
-- All inserts are ON CONFLICT DO NOTHING for idempotency.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

-- ============================================================
-- users — admin user + viewer user + invited (disabled) member
-- ============================================================
-- Admin user: active, with admin role
INSERT INTO users (
    id,
    tenant_id,
    email,
    display_name,
    status,
    idp_issuer,
    idp_subject,
    time_zone
)
VALUES (
    'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaa001',
    '00000000-0000-0000-0000-00000000d3a0',
    'admin-e2e@example.invalid',
    'Admin E2E User',
    'active',
    '',
    '',
    'America/Los_Angeles'
)
ON CONFLICT DO NOTHING;

-- Viewer user: active, with viewer role
INSERT INTO users (
    id,
    tenant_id,
    email,
    display_name,
    status,
    idp_issuer,
    idp_subject,
    time_zone
)
VALUES (
    'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbb01',
    '00000000-0000-0000-0000-00000000d3a0',
    'viewer-e2e@example.invalid',
    'Viewer E2E User',
    'active',
    '',
    '',
    'America/Los_Angeles'
)
ON CONFLICT DO NOTHING;

-- Invited member: disabled (not-yet-activated)
INSERT INTO users (
    id,
    tenant_id,
    email,
    display_name,
    status,
    idp_issuer,
    idp_subject,
    time_zone
)
VALUES (
    'cccccccc-cccc-cccc-cccc-cccccccccc01',
    '00000000-0000-0000-0000-00000000d3a0',
    'invited-e2e@example.invalid',
    'Invited E2E User',
    'disabled',
    '',
    '',
    'America/Los_Angeles'
)
ON CONFLICT DO NOTHING;

-- ============================================================
-- user_roles — RBAC assignments for admin and viewer users
-- ============================================================
-- Admin user gets the 'admin' role
INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES (
    '00000000-0000-0000-0000-00000000d3a0',
    'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaa001',
    'admin',
    'slice-115-fixture'
)
ON CONFLICT DO NOTHING;

-- Viewer user gets the 'viewer' role (and grc_engineer as secondary)
INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES (
    '00000000-0000-0000-0000-00000000d3a0',
    'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbb01',
    'viewer',
    'slice-115-fixture'
)
ON CONFLICT DO NOTHING;

INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES (
    '00000000-0000-0000-0000-00000000d3a0',
    'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbb01',
    'grc_engineer',
    'slice-115-fixture'
)
ON CONFLICT DO NOTHING;

-- Invited member gets viewer role (disabled account, so roles have no immediate effect but represent intent)
INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES (
    '00000000-0000-0000-0000-00000000d3a0',
    'cccccccc-cccc-cccc-cccc-cccccccccc01',
    'viewer',
    'slice-115-fixture'
)
ON CONFLICT DO NOTHING;

-- ============================================================
-- oidc_idp_configs — OIDC connection for SSO test
-- ============================================================
-- Deterministic OIDC configuration for the admin-bootstrap SSO test.
-- The test fills in these values from the fixture and verifies the UI
-- round-trips them correctly (write-once client_secret, etc.).
-- client_secret_enc is a BYTEA column. We encrypt a test secret using
-- a deterministic approach (the column stores encrypted data, but the
-- exact encryption doesn't matter for this fixture — it just needs to
-- be a valid BYTEA value).
INSERT INTO oidc_idp_configs (
    id,
    tenant_id,
    name,
    issuer_url,
    client_id,
    client_secret_enc,
    redirect_url,
    allowed_email_domains
)
VALUES (
    'dddddddd-dddd-dddd-dddd-ddddddddd001',
    '00000000-0000-0000-0000-00000000d3a0',
    'demo-idp',
    'https://idp.example.com',
    'platform-rp-client-id',
    decode('0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20', 'hex'),
    'https://atlas.example.com/auth/oidc/callback',
    ARRAY['example.com', 'example.org']::TEXT[]
)
ON CONFLICT DO NOTHING;

-- ============================================================
-- Feature flag (admin UI toggle target)
-- ============================================================
-- The slice-019 feature_flags table is tenant-scoped with a composite
-- PK (tenant_id, flag_key). Insert one flag from each of two
-- categories so the admin UI's category grouping has data to render.
INSERT INTO feature_flags (tenant_id, flag_key, enabled, description, category)
VALUES
(
    '00000000-0000-0000-0000-00000000d3a0',
    'demo-flag-board-pack-export',
    FALSE,
    'Demo feature flag for the slice-082 e2e admin-bootstrap spec (board category).',
    'board'
),
(
    '00000000-0000-0000-0000-00000000d3a0',
    'demo-flag-risk-aggregation-v2',
    TRUE,
    'Demo feature flag for the slice-082 e2e admin-bootstrap spec (risk category).',
    'risk'
)
ON CONFLICT DO NOTHING;

COMMIT;
