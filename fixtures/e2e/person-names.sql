-- #2085 — Playwright e2e seed for `web/e2e/person-names.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). One active user of this spec's own, in the demo tenant, who
-- owns an action plan, created an audit period (as a `user:` subject) and
-- completed a personnel checklist item. Each page should show the user's
-- name, never the ID.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

INSERT INTO users (
    id, tenant_id, email, display_name, status, idp_issuer, idp_subject
)
VALUES (
    '44444444-4444-4444-4444-444444442085',
    '00000000-0000-0000-0000-00000000d3a0',
    'person-names-e2e@example.invalid',
    'Person Names E2E',
    'active',
    '',
    ''
)
ON CONFLICT (id) DO UPDATE
    SET status = 'active', display_name = 'Person Names E2E';

INSERT INTO action_plans (id, tenant_id, title, owner_id, due_date, status)
VALUES (
    'acacacac-acac-acac-acac-acacacac2085',
    '00000000-0000-0000-0000-00000000d3a0',
    'Person names e2e plan (2085)',
    '44444444-4444-4444-4444-444444442085',
    (now() + INTERVAL '30 days')::date,
    'in_progress'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO audit_periods (
    id, tenant_id, name, framework_version_id, period_start, period_end,
    status, created_by
)
VALUES (
    'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbb2085',
    '00000000-0000-0000-0000-00000000d3a0',
    'Person names e2e period (2085)',
    '11111111-1111-1111-1111-111111110002',
    '2026-10-01',
    '2026-12-31',
    'open',
    'user:44444444-4444-4444-4444-444444442085'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO personnel_security_checklists (
    id, tenant_id, workflow_kind, source, person_external_id,
    person_display_name, due_at, status, created_by
)
VALUES (
    'cdcdcdcd-cdcd-cdcd-cdcd-cdcdcdcd2085',
    '00000000-0000-0000-0000-00000000d3a0',
    'onboarding',
    'manual',
    'e2e-person-2085',
    'Onboardee 2085',
    now() + INTERVAL '14 days',
    'open',
    'e2e:person-names'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO personnel_security_checklist_items (
    id, tenant_id, checklist_id, slug, title, category, sort_order,
    completed_at, completed_by
)
VALUES (
    'cececece-cece-cece-cece-cecececec085',
    '00000000-0000-0000-0000-00000000d3a0',
    'cdcdcdcd-cdcd-cdcd-cdcd-cdcdcdcd2085',
    'e2e-person-names',
    'Person names e2e item',
    'access',
    0,
    now(),
    '44444444-4444-4444-4444-444444442085'
)
ON CONFLICT (id) DO NOTHING;

COMMIT;
