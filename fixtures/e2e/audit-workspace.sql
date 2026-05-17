-- Slice 082 — Playwright e2e seed for `web/e2e/audit-workspace.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The audit-workspace spec's preconditions (per its preamble):
--
--   - TEST_AUDITOR_BEARER carries a credential with an
--     auditor_assignment to a known, frozen AuditPeriod
--   - TEST_AUDITEE_BEARER carries a non-auditor credential in the same
--     tenant (for the P0-2 private-note assertion)
--   - the period has at least one control with evidence in-window
--
-- All inserts are ON CONFLICT DO NOTHING for idempotency.
--
-- Reuses the audit_period (55555555-...-0001) from
-- fixtures/walkthroughs/audit-period.sql, which the harness also
-- applies. The period is FROZEN here for the audit-workspace spec.

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
-- user_id is a TEXT column here (not a UUID FK); we use the demo email.
INSERT INTO auditor_assignments (
    tenant_id, user_id, audit_period_id, granted_by
)
VALUES (
    '00000000-0000-0000-0000-00000000d3a0',
    'demo-operator@example.invalid',
    'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbb0001',
    'demo-operator@example.invalid'
)
ON CONFLICT DO NOTHING;

COMMIT;
