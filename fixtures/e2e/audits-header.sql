-- Slice 213 — Playwright e2e seed for `web/e2e/audits-header.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The audits-header spec's preconditions:
--
--   - At least one audit period with status='in_progress' (drives the
--     "pill renders the period name" assertion).
--   - The in-progress period uses a deterministic name so the test can
--     assert exact copy.
--   - The base settings.sql fixture supplies the users row that
--     /v1/me resolves to (needed for the avatar). The audits-header
--     spec drives the user-avatar assertion through the same DEMO_USER
--     identity; we keep settings-style seeding here in case the spec
--     ever needs it standalone, but the bare minimum is the period.
--
-- The walkthroughs/audit-period.sql fixture seeds a separate period
-- ('SOC2 Q1 2026', status='open'); we insert our own row with a
-- distinct UUID + a distinct name so the pill query has exactly ONE
-- in_progress match to pick.
--
-- audit_periods.status was originally constrained to ('open','frozen')
-- in v1; the slice 215 work and downstream slices anticipate the
-- broader vocabulary {planned/in_progress/closed} arriving as the
-- backend lifts the CHECK constraint. If the CHECK constraint is
-- still narrow when this fixture runs, the INSERT will fail loudly
-- (ON ERROR STOP) and the test surfaces the gap — better than
-- silently seeding an 'open' period that doesn't match the filter.
--
-- All inserts are ON CONFLICT DO NOTHING for idempotency.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

-- Slice 164/108: the users row /v1/me resolves to. Mirrors the
-- settings fixture insert so this spec can drive the avatar assertion
-- without depending on the settings spec's seed running first.
INSERT INTO users (
    id, tenant_id, email, display_name, status, idp_issuer, idp_subject
)
VALUES (
    '44444444-4444-4444-4444-444444440001',
    '00000000-0000-0000-0000-00000000d3a0',
    'demo-operator@example.invalid',
    'Sam Operator',
    'active',
    'urn:atlas:test',
    'demo-operator-subject'
)
ON CONFLICT DO NOTHING;

-- In-progress period for the pill assertion. Name is deterministic so
-- the spec can pin the visible copy.
INSERT INTO audit_periods (
    id, tenant_id, name, framework_version_id, period_start, period_end,
    status, created_by
)
VALUES (
    'cccccccc-cccc-cccc-cccc-cccccccc0001',
    '00000000-0000-0000-0000-00000000d3a0',
    'SOC 2 Type II · Q2 2026',
    '11111111-1111-1111-1111-111111110002',
    '2026-04-01',
    '2026-06-30',
    'in_progress',
    'demo-operator@example.invalid'
)
ON CONFLICT DO NOTHING;

COMMIT;
