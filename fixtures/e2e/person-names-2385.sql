-- #2385 — Playwright e2e seed for `web/e2e/person-names-2385.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). One active user of this spec's own, in the demo tenant, who
-- requested an exception (recorded as a `user:` subject, the form a JWT
-- session writes), whose request is in the exception history the
-- dashboard feed and the activity page read, and who published a board
-- pack. Each page should show the user's name, never the ID.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

INSERT INTO users (
    id, tenant_id, email, display_name, status, idp_issuer, idp_subject
)
VALUES (
    '55555555-5555-4555-8555-555555552385',
    '00000000-0000-0000-0000-00000000d3a0',
    'person-names-2385@example.invalid',
    'Remaining Pages E2E',
    'active',
    '',
    ''
)
ON CONFLICT (id) DO UPDATE
    SET status = 'active', display_name = 'Remaining Pages E2E';

INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES ('00000000-0000-0000-0000-00000000d3a0', '55555555-5555-4555-8555-555555552385', 'grc_engineer', 'e2e:2385')
ON CONFLICT DO NOTHING;

-- A requested exception: requested_by is the user's `user:` subject.
-- requested_at moves to now() on every run so the row sorts first.
INSERT INTO exceptions (
    id, tenant_id, control_id, scope_cell_predicate, justification,
    compensating_controls, requested_by, requested_at, expires_at, status
)
VALUES (
    'e2385e23-85e2-485e-8385-e2385e2385e2',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330001',
    '{}'::jsonb,
    'Person names e2e exception (2385).',
    ARRAY[]::text[],
    'user:55555555-5555-4555-8555-555555552385',
    now(),
    now() + INTERVAL '30 days',
    'requested'
)
ON CONFLICT (id) DO UPDATE
    SET requested_at = EXCLUDED.requested_at,
        expires_at   = EXCLUDED.expires_at;

-- Ten history rows by the user, the newest at now(): the dashboard feed
-- shows the ten newest events, and other specs' writes between this
-- seed and the dashboard visit must not push all of them out.
DELETE FROM exception_audit_log
WHERE exception_id = 'e2385e23-85e2-485e-8385-e2385e2385e2';

INSERT INTO exception_audit_log (
    id, tenant_id, exception_id, action, actor, from_state, to_state,
    occurred_at
)
SELECT
    gen_random_uuid(),
    '00000000-0000-0000-0000-00000000d3a0',
    'e2385e23-85e2-485e-8385-e2385e2385e2',
    'requested',
    'user:55555555-5555-4555-8555-555555552385',
    NULL,
    'requested',
    now() - (n * INTERVAL '1 second')
FROM generate_series(0, 9) AS n;

-- A published board pack whose published_by is the user's ID.
INSERT INTO board_packs (
    id, tenant_id, period_end, status, content, narrative_md,
    published_by, published_at
)
VALUES (
    'b2385b23-85b2-485b-8385-b2385b2385b2',
    '00000000-0000-0000-0000-00000000d3a0',
    '2025-06-30',
    'published',
    jsonb_build_object(
        'period_end', '2025-06-30',
        'generated_at', '2025-07-01T00:00:00Z',
        'status', 'published',
        'sections', '{}'::jsonb
    ),
    'Person names e2e board pack (2385).',
    '55555555-5555-4555-8555-555555552385',
    '2025-07-02T00:00:00Z'
)
ON CONFLICT (id) DO NOTHING;

COMMIT;
