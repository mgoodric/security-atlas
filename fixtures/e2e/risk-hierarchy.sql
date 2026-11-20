-- Slice 082 — Playwright e2e seed for `web/e2e/risk-hierarchy.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). #2755 moved the rows out of the demo tenant into two tenants of
-- the spec's own (UUID suffix 2755b03…), so the timeline's row counts and
-- the empty states never read rows other specs add to the demo tenant.
--
-- Tenant H (…2755b0300001), the populated hierarchy:
--   - two org_units in a parent/child relationship (Engineering > Platform
--     Team);
--   - a tenant-private theme beside the 10 default themes (migration _015);
--   - one active aggregation rule targeting the default `ownership` theme;
--   - three decisions: `active` with a future revisit_by, `active` with a
--     past revisit_by (the amber overdue pill), and `superseded` by the
--     first.
--
-- Tenant E (…2755b0300002) has no org_units, themed risks or decisions,
-- for the per-panel empty states.
--
-- All inserts are ON CONFLICT DO NOTHING for idempotency. Decision dates
-- are relative to the seeding day, so the overdue / not-overdue split
-- holds on every run.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-2755b0300001';

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-2755b0300001', 'Risk hierarchy (e2e)', false)
ON CONFLICT DO NOTHING;

-- ============================================================
-- Org units: parent + child
-- ============================================================
INSERT INTO org_units (id, tenant_id, name, parent_id, level)
VALUES
    ('00000000-0000-0000-0000-2755b0300101', '00000000-0000-0000-0000-2755b0300001',
     'Engineering', NULL, 'org'),
    ('00000000-0000-0000-0000-2755b0300102', '00000000-0000-0000-0000-2755b0300001',
     'Platform Team', '00000000-0000-0000-0000-2755b0300101', 'team')
ON CONFLICT DO NOTHING;

-- ============================================================
-- Tenant-private theme (sibling to the 10 default themes from _015)
-- ============================================================
INSERT INTO org_themes (id, tenant_id, theme_name, description)
VALUES ('00000000-0000-0000-0000-2755b0300201', '00000000-0000-0000-0000-2755b0300001',
        'acme-private-theme', 'Tenant-private theme used by the risk-hierarchy e2e fixture.')
ON CONFLICT DO NOTHING;

-- ============================================================
-- Aggregation rule targeting a default theme
-- ============================================================
-- 'ownership' is one of the 10 default themes from migration _015. The API
-- reads a rule from its rule_body document (aggrule.Rule), not from the
-- columns, so the body restates them; the upsert repairs a row an older
-- copy of this fixture wrote with a bare '{"version": 1}' body.
INSERT INTO aggregation_rules (
    id, tenant_id, rule_id, target_theme, min_risks, min_teams,
    window_days, parent_level, severity_function, rule_body, status,
    activated_by, activated_at
)
VALUES (
    '00000000-0000-0000-0000-2755b0300301',
    '00000000-0000-0000-0000-2755b0300001',
    'ownership-aggregation-2026',
    'ownership',
    3,
    2,
    90,
    'org',
    'max',
    '{"rule_id": "ownership-aggregation-2026", "target_theme": "ownership",
      "min_risks": 3, "min_teams": 2, "window_days": 90,
      "parent_level": "org", "severity_function": "max"}'::jsonb,
    'active',
    'demo-operator@example.invalid',
    now()
)
ON CONFLICT (id) DO UPDATE SET rule_body = EXCLUDED.rule_body;

-- ============================================================
-- Decisions: future revisit, overdue revisit, superseded
-- ============================================================
INSERT INTO decisions (
    id, tenant_id, decision_id, title, narrative, decision_maker,
    decided_at, revisit_by, status, superseded_by
)
VALUES
    ('00000000-0000-0000-0000-2755b0300401', '00000000-0000-0000-0000-2755b0300001',
     'dec-future-2026', 'Adopt managed KMS for encryption-at-rest',
     'Standardize on one managed key service for production object-store encryption.',
     'demo-operator@example.invalid',
     now() - INTERVAL '30 days', (CURRENT_DATE + INTERVAL '90 days')::date, 'active', NULL),
    ('00000000-0000-0000-0000-2755b0300402', '00000000-0000-0000-0000-2755b0300001',
     'dec-overdue-2025', 'Quarterly access-review cadence',
     'Run access reviews quarterly per SOC 2 Common Criteria.',
     'demo-operator@example.invalid',
     now() - INTERVAL '365 days', (CURRENT_DATE - INTERVAL '30 days')::date, 'active', NULL),
    ('00000000-0000-0000-0000-2755b0300403', '00000000-0000-0000-0000-2755b0300001',
     'dec-superseded-2025', 'Self-managed keys for encryption-at-rest',
     'Replaced by the managed key service decision.',
     'demo-operator@example.invalid',
     now() - INTERVAL '400 days', NULL, 'superseded',
     '00000000-0000-0000-0000-2755b0300401')
ON CONFLICT DO NOTHING;

COMMIT;

-- ============================================================
-- Tenant E: nothing but the tenant
-- ============================================================
BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-2755b0300002';

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-2755b0300002', 'Risk hierarchy empty (e2e)', false)
ON CONFLICT DO NOTHING;

COMMIT;
