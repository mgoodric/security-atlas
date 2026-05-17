-- Slice 082 — Playwright e2e seed for `web/e2e/risk-hierarchy.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The risk-hierarchy spec's preconditions (per its preamble):
--
--   - at least two org_units in a parent/child relationship
--   - the 10 default themes (slice 053 seed, already loaded by
--     migration _015) plus optionally a tenant-private theme
--   - at least one active aggregation rule targeting a theme
--   - at least one decision with a future revisit_by
--   - at least one decision whose revisit_by is in the past (amber pill)
--
-- All inserts are ON CONFLICT DO NOTHING for idempotency.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

-- ============================================================
-- Org units: parent + child
-- ============================================================
INSERT INTO org_units (id, tenant_id, name, parent_id, level)
VALUES
(
    'cccccccc-cccc-cccc-cccc-cccccccc0001',
    '00000000-0000-0000-0000-00000000d3a0',
    'Engineering',
    NULL,
    'org'
),
(
    'cccccccc-cccc-cccc-cccc-cccccccc0002',
    '00000000-0000-0000-0000-00000000d3a0',
    'Platform Team',
    'cccccccc-cccc-cccc-cccc-cccccccc0001',
    'team'
)
ON CONFLICT DO NOTHING;

-- ============================================================
-- Tenant-private theme (sibling to the 10 default themes from _015)
-- ============================================================
INSERT INTO org_themes (id, tenant_id, theme_name, description)
VALUES (
    'dddddddd-dddd-dddd-dddd-dddddddd0001',
    '00000000-0000-0000-0000-00000000d3a0',
    'acme-private-theme',
    'Tenant-private theme used by the slice-082 e2e fixture.'
)
ON CONFLICT DO NOTHING;

-- ============================================================
-- Aggregation rule targeting a default theme
-- ============================================================
-- 'ownership' is one of the 10 default themes from migration _015.
INSERT INTO aggregation_rules (
    id, tenant_id, rule_id, target_theme, min_risks, min_teams,
    window_days, parent_level, severity_function, rule_body, status,
    activated_by, activated_at
)
VALUES (
    'eeeeeeee-eeee-eeee-eeee-eeeeeeee0001',
    '00000000-0000-0000-0000-00000000d3a0',
    'ownership-aggregation-2026',
    'ownership',
    3,
    2,
    90,
    'org',
    'max',
    '{"version": 1}'::jsonb,
    'active',
    'demo-operator@example.invalid',
    now()
)
ON CONFLICT DO NOTHING;

-- ============================================================
-- Two decisions: one with future revisit_by, one overdue
-- ============================================================
INSERT INTO decisions (
    id, tenant_id, decision_id, title, narrative, decision_maker,
    decided_at, revisit_by, status
)
VALUES
(
    'ffffffff-ffff-ffff-ffff-ffffffff0001',
    '00000000-0000-0000-0000-00000000d3a0',
    'dec-future-2026',
    'Adopt managed KMS for encryption-at-rest',
    'Standardize on AWS KMS for all production object-store encryption.',
    'demo-operator@example.invalid',
    now() - INTERVAL '30 days',
    (CURRENT_DATE + INTERVAL '90 days')::date,
    'active'
),
(
    'ffffffff-ffff-ffff-ffff-ffffffff0002',
    '00000000-0000-0000-0000-00000000d3a0',
    'dec-overdue-2025',
    'Quarterly access-review cadence',
    'Run access reviews quarterly per SOC 2 Common Criteria.',
    'demo-operator@example.invalid',
    now() - INTERVAL '365 days',
    (CURRENT_DATE - INTERVAL '30 days')::date,
    'active'
)
ON CONFLICT DO NOTHING;

COMMIT;
