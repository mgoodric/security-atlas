-- Slice 213 — Playwright e2e seed for `web/e2e/audits-header.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The audits-header spec's preconditions:
--
--   - At least one audit period in the "active" (open + not-yet-frozen)
--     state, with a deterministic name so the spec can pin the visible
--     copy. v1 DB CHECK constraint allows status IN ('open','frozen');
--     `'open'` is the v1 status that maps to the user-facing "in
--     progress" pill (see `in-progress-audit-pill.tsx` header comment
--     for the rationale).
--   - The base settings.sql fixture supplies the users row that
--     /v1/me resolves to (needed for the avatar). The audits-header
--     spec drives the user-avatar assertion through the same DEMO_USER
--     identity; we keep settings-style seeding here in case the spec
--     ever needs it standalone, but the bare minimum is the period.
--
-- The walkthroughs/audit-period.sql fixture seeds a separate period
-- ('SOC2 Q1 2026', status='open'); we insert our own row with a
-- distinct UUID + a distinct name so the pill query has a clear winner
-- to pick (`pickMostRecentInProgress` returns the latest period_start
-- among the open ones; this row's period_start is 2026-04-01 which is
-- later than the walkthroughs seed's period_start).
--
-- All inserts are ON CONFLICT DO NOTHING for idempotency.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

-- Slice 164/108: the users row /v1/me resolves to. Mirrors the
-- settings fixture insert so this spec can drive the avatar assertion
-- without depending on the settings spec's seed running first.
--
-- IMPORTANT: this row shares its id with the settings.sql fixture
-- insert (both use 44444444-...0001). ON CONFLICT DO NOTHING means
-- whichever runs first wins; the loser's values are masked. To avoid
-- silently breaking the settings spec's AC-8 (time_zone round-trip),
-- this insert ALSO carries time_zone='America/New_York' — the same
-- value settings.sql expects. The display_name divergence ('Sam
-- Operator' vs settings.sql's 'Settings E2E Operator') is benign
-- because settings.spec.ts does NOT pin display_name; only the
-- audits-header avatar AC-4 does.
INSERT INTO users (
    id, tenant_id, email, display_name, status, idp_issuer, idp_subject,
    time_zone
)
VALUES (
    '44444444-4444-4444-4444-444444440001',
    '00000000-0000-0000-0000-00000000d3a0',
    'demo-operator@example.invalid',
    'Sam Operator',
    'active',
    'urn:atlas:test',
    'demo-operator-subject',
    'America/New_York'
)
ON CONFLICT DO NOTHING;

-- Active (open, not-yet-frozen) period for the pill assertion. Name is
-- deterministic so the spec can pin the visible copy.
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
    'open',
    'demo-operator@example.invalid'
)
ON CONFLICT DO NOTHING;

COMMIT;
