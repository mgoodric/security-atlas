-- #2145 — Playwright e2e seed for `web/e2e/onboarding-hints.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). Hint dismissals are stored per user, and the rest of the suite
-- runs fullyParallel as the one demo user, so the spec signs in as this
-- user of its own (a JWT minted for HINTS_USER_ID in web/e2e/seed.ts).
--
-- Every run deletes the user's dismissals, so the spec starts from a first
-- visit each time.

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
    '44444444-4444-4444-4444-444444442145',
    '00000000-0000-0000-0000-00000000d3a0',
    'hints-e2e-user@example.invalid',
    'Hints E2E Operator',
    'active',
    '',
    ''
)
ON CONFLICT (id) DO NOTHING;

DELETE FROM user_onboarding_hint_dismissals
WHERE tenant_id = '00000000-0000-0000-0000-00000000d3a0'
  AND user_id = '44444444-4444-4444-4444-444444442145';

COMMIT;
