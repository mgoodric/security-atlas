-- #2142 — Playwright e2e seed for `web/e2e/settings-theme.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The theme is stored per user, and the rest of the suite runs
-- fullyParallel as the one demo user, several specs forcing a theme
-- through the browser cache. A theme spec that wrote the demo user's
-- stored theme would leak into them, so it signs in as this user of its
-- own instead (a JWT minted for THEME_USER_ID in web/e2e/seed.ts).
--
-- Every run resets the stored theme to '' (never chosen), so the spec
-- starts from the default each time.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

INSERT INTO users (
    id,
    tenant_id,
    email,
    display_name,
    status,
    idp_issuer,
    idp_subject
)
VALUES (
    '44444444-4444-4444-4444-444444442142',
    '00000000-0000-0000-0000-00000000d3a0',
    'theme-e2e-user@example.invalid',
    'Theme E2E Operator',
    'active',
    '',
    ''
)
ON CONFLICT (id) DO UPDATE SET theme = '';

COMMIT;
