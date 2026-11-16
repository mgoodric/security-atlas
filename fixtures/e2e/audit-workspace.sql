-- #2157 — Playwright e2e seed for `web/e2e/audit-workspace.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The spec's rows live in a tenant of their own
-- (...2157, "Audit Workspace E2E"), so no other spec's controls,
-- evidence or periods change; a first cut in the demo tenant added two
-- controls and broke controls-list.spec.ts's row count. In that tenant:
--
--   - a global framework and version for the periods to cite;
--   - two open audit periods, "assigned" (the spec assigns its auditor to
--     it) and "unassigned";
--   - an active admin and an active auditor (user_roles rows); every run
--     drops the auditor's assignments, so the spec starts unassigned;
--   - two scope cells, environment prod and dev, each with its scopes
--     mirror row (the evidence_records.scope_id FK target);
--   - control A with a population selecting only the prod cell, attached
--     to the assigned period, and control B with a match-every-cell
--     population attached to the unassigned period;
--   - evidence: A in prod (in scope), A in dev (a cell the population does
--     not select), B in prod (the unassigned period's).
--
-- It also keeps slice 082's frozen demo-tenant period, which
-- web/e2e/fixtures.ts exports as seeded.auditPeriodId and
-- route-sweep.spec.ts opens. Its old assignment (to an email, which no
-- principal carries) is gone.
--
-- Rows are ON CONFLICT DO NOTHING, so reruns are idempotent. Samples the
-- spec draws accumulate across runs; nothing reads old ones.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

INSERT INTO audit_periods (
    id, tenant_id, name, framework_version_id, period_start, period_end,
    status, frozen_at, frozen_hash, frozen_by, created_by
)
VALUES (
    'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbb0001',
    '00000000-0000-0000-0000-00000000d3a0',
    'SOC 2 2026 Q2',
    '11111111-1111-1111-1111-111111110002',
    '2026-04-01',
    '2026-06-30',
    'frozen',
    '2026-07-01T00:00:00Z',
    decode('00112233445566778899aabbccddeeff', 'hex'),
    'demo-operator@example.invalid',
    'demo-operator@example.invalid'
)
ON CONFLICT DO NOTHING;

COMMIT;

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-000000002157';

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-000000002157', 'Audit Workspace E2E', FALSE)
ON CONFLICT (id) DO NOTHING;

INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
VALUES (
    '11111111-1111-1111-1111-111111112157',
    NULL,
    'e2e-audit-workspace-2157',
    'Audit workspace e2e framework (2157)',
    'e2e',
    'Synthetic framework for the audit-workspace spec only.'
)
ON CONFLICT DO NOTHING;

INSERT INTO framework_versions (id, tenant_id, framework_id, version)
VALUES (
    '11111111-1111-1111-1111-111111112158',
    NULL,
    '11111111-1111-1111-1111-111111112157',
    '1.0'
)
ON CONFLICT DO NOTHING;

INSERT INTO audit_periods (
    id, tenant_id, name, framework_version_id, period_start, period_end,
    status, created_by
)
VALUES
    ('bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbb2157',
     '00000000-0000-0000-0000-000000002157',
     'Audit workspace e2e (2157)',
     '11111111-1111-1111-1111-111111112158',
     '2026-01-01', '2026-03-31', 'open', 'e2e:audit-workspace'),
    ('bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbc2157',
     '00000000-0000-0000-0000-000000002157',
     'Audit workspace e2e, unassigned (2157)',
     '11111111-1111-1111-1111-111111112158',
     '2026-01-01', '2026-03-31', 'open', 'e2e:audit-workspace')
ON CONFLICT (id) DO NOTHING;

INSERT INTO users (
    id, tenant_id, email, display_name, status, idp_issuer, idp_subject
)
VALUES
    ('44444444-4444-4444-4444-444444442157',
     '00000000-0000-0000-0000-000000002157',
     'audit-workspace-e2e@example.invalid',
     'Audit Workspace E2E', 'active', '', ''),
    ('44444444-4444-4444-4444-444444442158',
     '00000000-0000-0000-0000-000000002157',
     'audit-workspace-admin-e2e@example.invalid',
     'Audit Workspace Admin E2E', 'active', '', '')
ON CONFLICT (id) DO UPDATE SET status = 'active';

INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES
    ('00000000-0000-0000-0000-000000002157',
     '44444444-4444-4444-4444-444444442157', 'auditor', 'e2e:audit-workspace'),
    ('00000000-0000-0000-0000-000000002157',
     '44444444-4444-4444-4444-444444442158', 'admin', 'e2e:audit-workspace')
ON CONFLICT DO NOTHING;

DELETE FROM auditor_assignments
WHERE tenant_id = '00000000-0000-0000-0000-000000002157'
  AND user_id   = '44444444-4444-4444-4444-444444442157';

INSERT INTO scope_cells (id, tenant_id, label, dimensions, dimensions_hash)
VALUES
    ('22222222-2222-2222-2222-22222222a157',
     '00000000-0000-0000-0000-000000002157',
     'e2e 2157 prod', '{"environment":"prod"}'::jsonb, 'e2e-2157-prod'),
    ('22222222-2222-2222-2222-22222222b157',
     '00000000-0000-0000-0000-000000002157',
     'e2e 2157 dev', '{"environment":"dev"}'::jsonb, 'e2e-2157-dev')
ON CONFLICT DO NOTHING;

INSERT INTO scopes (id, tenant_id)
VALUES
    ('22222222-2222-2222-2222-22222222a157', '00000000-0000-0000-0000-000000002157'),
    ('22222222-2222-2222-2222-22222222b157', '00000000-0000-0000-0000-000000002157')
ON CONFLICT DO NOTHING;

INSERT INTO controls (
    id, tenant_id, title, description, control_family,
    implementation_type, owner_role, lifecycle_state, applicability_expr,
    bundle_id, freshness_class
)
VALUES
    ('33333333-3333-3333-3333-33333333a157',
     '00000000-0000-0000-0000-000000002157',
     'Audit workspace e2e control A (2157)',
     'Sampled in the assigned period, prod cell only.',
     'Asset Management', 'automated', 'security-engineering', 'active',
     'true', 'e2e-audit-workspace-a', 'monthly'),
    ('33333333-3333-3333-3333-33333333b157',
     '00000000-0000-0000-0000-000000002157',
     'Audit workspace e2e control B (2157)',
     'Sampled in the unassigned period.',
     'Asset Management', 'automated', 'security-engineering', 'active',
     'true', 'e2e-audit-workspace-b', 'monthly')
ON CONFLICT DO NOTHING;

INSERT INTO evidence_records (
    id, tenant_id, control_id, scope_id, observed_at, provenance, result,
    payload, hash, control_ref
)
VALUES
    ('66666666-6666-6666-6666-66666666a157',
     '00000000-0000-0000-0000-000000002157',
     '33333333-3333-3333-3333-33333333a157',
     '22222222-2222-2222-2222-22222222a157',
     '2026-02-01T12:00:00Z', '{"source":"e2e-fixture"}'::jsonb, 'pass',
     '{}'::jsonb, 'sha256-audit-workspace-2157-in-scope',
     '33333333-3333-3333-3333-33333333a157'),
    ('66666666-6666-6666-6666-66666666b157',
     '00000000-0000-0000-0000-000000002157',
     '33333333-3333-3333-3333-33333333a157',
     '22222222-2222-2222-2222-22222222b157',
     '2026-02-01T12:00:00Z', '{"source":"e2e-fixture"}'::jsonb, 'pass',
     '{}'::jsonb, 'sha256-audit-workspace-2157-other-cell',
     '33333333-3333-3333-3333-33333333a157'),
    ('66666666-6666-6666-6666-66666666c157',
     '00000000-0000-0000-0000-000000002157',
     '33333333-3333-3333-3333-33333333b157',
     '22222222-2222-2222-2222-22222222a157',
     '2026-02-01T12:00:00Z', '{"source":"e2e-fixture"}'::jsonb, 'pass',
     '{}'::jsonb, 'sha256-audit-workspace-2157-unassigned',
     '33333333-3333-3333-3333-33333333b157')
ON CONFLICT DO NOTHING;

INSERT INTO populations (
    id, tenant_id, control_id, scope_predicate, time_window_start,
    time_window_end, row_count, created_by, audit_period_id
)
VALUES
    ('77777777-7777-7777-7777-77777777a157',
     '00000000-0000-0000-0000-000000002157',
     '33333333-3333-3333-3333-33333333a157',
     '{"op":"eq","dim":"environment","value":"prod"}'::jsonb,
     '2026-01-01T00:00:00Z', '2026-03-31T23:59:59Z', 1,
     'e2e:audit-workspace', 'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbb2157'),
    ('77777777-7777-7777-7777-77777777b157',
     '00000000-0000-0000-0000-000000002157',
     '33333333-3333-3333-3333-33333333b157',
     '{"op":"true"}'::jsonb,
     '2026-01-01T00:00:00Z', '2026-03-31T23:59:59Z', 1,
     'e2e:audit-workspace', 'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbc2157')
ON CONFLICT DO NOTHING;

COMMIT;
