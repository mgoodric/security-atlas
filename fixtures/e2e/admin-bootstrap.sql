-- Slice 082 — Playwright e2e seed for `web/e2e/admin-bootstrap.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The admin-bootstrap spec's preconditions (per its preamble):
--
--   - TEST_ADMIN_BEARER carries an admin credential (the harness'
--     api_keys insert handles this — is_admin=true)
--   - the platform was seeded with at least one feature flag
--   - the platform exposes a test-discovery-doc HTTP endpoint for the
--     SSO preflight (or the test points at a known public IdP doc)
--
-- All inserts are ON CONFLICT DO NOTHING for idempotency.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

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
