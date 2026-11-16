-- #2118 — Playwright e2e seed for `web/e2e/period-auditors.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). One open audit period and one active user holding the
-- auditor role, both of this spec's own, in the demo tenant. Every run
-- drops the user's assignments, so the spec starts with the auditor
-- unassigned. The period's audit_period_audit_log rows are append-only
-- and accumulate across runs; nothing reads them here.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

INSERT INTO audit_periods (
    id, tenant_id, name, framework_version_id, period_start, period_end,
    status, created_by
)
VALUES (
    'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbb2118',
    '00000000-0000-0000-0000-00000000d3a0',
    'Auditor assignment e2e (2118)',
    '11111111-1111-1111-1111-111111110002',
    '2026-01-01',
    '2026-03-31',
    'open',
    'e2e:period-auditors'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO users (
    id, tenant_id, email, display_name, status, idp_issuer, idp_subject
)
VALUES (
    '44444444-4444-4444-4444-444444442118',
    '00000000-0000-0000-0000-00000000d3a0',
    'period-auditor-e2e@example.invalid',
    'Period Auditor E2E',
    'active',
    '',
    ''
)
ON CONFLICT (id) DO UPDATE SET status = 'active';

INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES (
    '00000000-0000-0000-0000-00000000d3a0',
    '44444444-4444-4444-4444-444444442118',
    'auditor',
    'e2e:period-auditors'
)
ON CONFLICT DO NOTHING;

DELETE FROM auditor_assignments
WHERE tenant_id = '00000000-0000-0000-0000-00000000d3a0'
  AND user_id   = '44444444-4444-4444-4444-444444442118';

COMMIT;
