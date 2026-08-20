-- Slice 082 — Playwright e2e seed for `web/e2e/audit-workspace.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The audit-workspace spec's preconditions (per its preamble):
--
--   - TEST_BEARER carries the demo user's credential with an
--     auditor_assignment to a known, frozen AuditPeriod
--   - the period has at least two controls with evidence in-window
--   - at least one evidence row is in-window and pre-freeze for sampling,
--     and one extra row is post-freeze so the frozen summary/sample surfaces
--     prove they are period-bounded
--
-- All inserts are ON CONFLICT DO NOTHING for idempotency.
--
-- Slice 113 extends this from STUB to FULL audit-workspace coverage.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

-- The audit_period from walkthroughs/audit-period.sql is named
-- "SOC2 Q1 2026"; the spec asserts "SOC 2 2026 Q2". Insert a SECOND
-- period for this spec rather than mutating the walkthroughs fixture
-- (which would couple the two test surfaces).
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

-- Auditor assignment: ties the test bearer's user_id to the period.
-- user_id is a TEXT column here; the e2e JWT uses DEMO_USER_ID as its
-- subject, not the demo email.
INSERT INTO auditor_assignments (
    tenant_id, user_id, audit_period_id, granted_by
)
VALUES (
    '00000000-0000-0000-0000-00000000d3a0',
    '44444444-4444-4444-4444-444444440001',
    'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbb0001',
    'demo-operator@example.invalid'
)
ON CONFLICT DO NOTHING;

-- Second assigned caller for the private-note negative assertion. This
-- user can resolve the same period, but auditor_only notes authored by
-- the demo user must be filtered out server-side.
INSERT INTO auditor_assignments (
    tenant_id, user_id, audit_period_id, granted_by
)
VALUES (
    '00000000-0000-0000-0000-00000000d3a0',
    '44444444-4444-4444-4444-444444440002',
    'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbb0001',
    'demo-operator@example.invalid'
)
ON CONFLICT DO NOTHING;

-- Second in-scope control so the workspace can switch between controls
-- without relying on global fixture state from another spec.
INSERT INTO controls (
    id, tenant_id, scf_id, title, description, control_family,
    implementation_type, owner_role, lifecycle_state, applicability_expr,
    bundle_id, freshness_class
)
VALUES (
    '33333333-3333-3333-3333-333333330002',
    '00000000-0000-0000-0000-00000000d3a0',
    'IAM-06',
    'Privileged access review — production administrators',
    'Production administrator access is reviewed and approved quarterly.',
    'Identity and Access Management',
    'manual_periodic',
    'security-engineering',
    'active',
    'env == "prod" AND data_classification == "confidential"',
    'demo-access-review',
    'monthly'
)
ON CONFLICT DO NOTHING;

-- Deterministic frozen-population evidence for the primary control.
-- The first five rows are inside the Q2 window and before frozen_at, so
-- population creation + sample draw have a non-empty bounded universe.
INSERT INTO evidence_records (
    id, tenant_id, control_id, control_ref, scope_id, observed_at, provenance,
    result, payload, hash, freshness_class, valid_until
)
VALUES
(
    'aaaaaaaa-1111-4111-8111-000000000001',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330001',
    'CRY-05',
    '22222222-2222-2222-2222-222222220001',
    '2026-04-15T12:00:00Z',
    '{"source":"e2e-audit-workspace","kind":"bucket-encryption"}'::jsonb,
    'pass',
    '{"bucket":"audit-workspace-primary","encrypted":true}'::jsonb,
    'audit-workspace-primary-001',
    'monthly',
    '2026-05-15T12:00:00Z'
),
(
    'aaaaaaaa-1111-4111-8111-000000000002',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330001',
    'CRY-05',
    '22222222-2222-2222-2222-222222220001',
    '2026-05-01T12:00:00Z',
    '{"source":"e2e-audit-workspace","kind":"bucket-encryption"}'::jsonb,
    'pass',
    '{"bucket":"audit-workspace-secondary","encrypted":true}'::jsonb,
    'audit-workspace-primary-002',
    'monthly',
    '2026-06-01T12:00:00Z'
),
(
    'aaaaaaaa-1111-4111-8111-000000000003',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330001',
    'CRY-05',
    '22222222-2222-2222-2222-222222220001',
    '2026-05-15T12:00:00Z',
    '{"source":"e2e-audit-workspace","kind":"bucket-encryption"}'::jsonb,
    'pass',
    '{"bucket":"audit-workspace-logs","encrypted":true}'::jsonb,
    'audit-workspace-primary-003',
    'monthly',
    '2026-06-15T12:00:00Z'
),
(
    'aaaaaaaa-1111-4111-8111-000000000004',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330001',
    'CRY-05',
    '22222222-2222-2222-2222-222222220001',
    '2026-06-01T12:00:00Z',
    '{"source":"e2e-audit-workspace","kind":"bucket-encryption"}'::jsonb,
    'pass',
    '{"bucket":"audit-workspace-archive","encrypted":true}'::jsonb,
    'audit-workspace-primary-004',
    'monthly',
    '2026-07-01T12:00:00Z'
),
(
    'aaaaaaaa-1111-4111-8111-000000000005',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330001',
    'CRY-05',
    '22222222-2222-2222-2222-222222220001',
    '2026-06-20T12:00:00Z',
    '{"source":"e2e-audit-workspace","kind":"bucket-encryption"}'::jsonb,
    'pass',
    '{"bucket":"audit-workspace-evidence","encrypted":true}'::jsonb,
    'audit-workspace-primary-005',
    'monthly',
    '2026-07-20T12:00:00Z'
)
ON CONFLICT DO NOTHING;

-- A post-freeze row for the same control. Period-scoped surfaces must not
-- include it because observed_at > frozen_at.
INSERT INTO evidence_records (
    id, tenant_id, control_id, control_ref, scope_id, observed_at, provenance,
    result, payload, hash, freshness_class, valid_until
)
VALUES (
    'aaaaaaaa-1111-4111-8111-000000000006',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330001',
    'CRY-05',
    '22222222-2222-2222-2222-222222220001',
    '2026-07-02T12:00:00Z',
    '{"source":"e2e-audit-workspace","kind":"bucket-encryption"}'::jsonb,
    'fail',
    '{"bucket":"audit-workspace-after-freeze","encrypted":false}'::jsonb,
    'audit-workspace-primary-post-freeze',
    'monthly',
    '2026-08-02T12:00:00Z'
)
ON CONFLICT DO NOTHING;

-- Evidence for the second control, enough to prove the nav/control switch
-- surface is backed by real rows rather than only a client-side UUID echo.
INSERT INTO evidence_records (
    id, tenant_id, control_id, control_ref, scope_id, observed_at, provenance,
    result, payload, hash, freshness_class, valid_until
)
VALUES
(
    'aaaaaaaa-1111-4111-8111-000000000101',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330002',
    'IAM-06',
    '22222222-2222-2222-2222-222222220001',
    '2026-05-10T12:00:00Z',
    '{"source":"e2e-audit-workspace","kind":"access-review"}'::jsonb,
    'pass',
    '{"review":"q2-admin-access","sampled_users":12}'::jsonb,
    'audit-workspace-secondary-001',
    'monthly',
    '2026-06-10T12:00:00Z'
),
(
    'aaaaaaaa-1111-4111-8111-000000000102',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330002',
    'IAM-06',
    '22222222-2222-2222-2222-222222220001',
    '2026-06-10T12:00:00Z',
    '{"source":"e2e-audit-workspace","kind":"access-review"}'::jsonb,
    'pass',
    '{"review":"q2-breakglass-access","sampled_users":2}'::jsonb,
    'audit-workspace-secondary-002',
    'monthly',
    '2026-07-10T12:00:00Z'
)
ON CONFLICT DO NOTHING;

COMMIT;
