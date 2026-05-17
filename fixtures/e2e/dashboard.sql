-- Slice 082 — Playwright e2e seed for `web/e2e/dashboard.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The dashboard spec's preconditions (per its preamble):
--
--   - at least one risk with treatment=mitigate (top-risks panel)
--   - at least one control that has drifted out of passing in the last
--     7 days (recent-drift panel)
--   - evidence records across >=2 freshness classes (freshness panel)
--   - at least one exception expiring within 30 days (upcoming panel)
--
-- All inserts are ON CONFLICT DO NOTHING for idempotency across re-runs.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

-- ============================================================
-- A risk with treatment=mitigate (linked to the seeded control)
-- ============================================================
INSERT INTO risks (
    id, tenant_id, title, description, category, treatment,
    treatment_owner, inherent_score, residual_score
)
VALUES (
    '77777777-7777-7777-7777-777777770001',
    '00000000-0000-0000-0000-00000000d3a0',
    'Unencrypted production data at rest',
    'Risk that production S3 buckets in customer-data accounts ship without server-side encryption.',
    'confidentiality',
    'mitigate',
    'security-engineering',
    '{"likelihood": 3, "impact": 4}'::jsonb,
    '{"likelihood": 1, "impact": 4}'::jsonb
)
ON CONFLICT DO NOTHING;

INSERT INTO risk_control_links (risk_id, control_id, tenant_id)
VALUES (
    '77777777-7777-7777-7777-777777770001',
    '33333333-3333-3333-3333-333333330001',
    '00000000-0000-0000-0000-00000000d3a0'
)
ON CONFLICT DO NOTHING;

-- ============================================================
-- Drift: two snapshots, yesterday + today, where today has fewer
-- passing controls than yesterday (i.e. control drifted out)
-- ============================================================
INSERT INTO control_drift_snapshots (
    id, tenant_id, snapshot_date, controls_passing, passing_control_ids,
    captured_at, trigger
)
VALUES
(
    '88888888-8888-8888-8888-888888880001',
    '00000000-0000-0000-0000-00000000d3a0',
    (CURRENT_DATE - INTERVAL '1 day')::date,
    1,
    ARRAY['33333333-3333-3333-3333-333333330001'::uuid],
    now() - INTERVAL '1 day',
    'scheduled'
),
(
    '88888888-8888-8888-8888-888888880002',
    '00000000-0000-0000-0000-00000000d3a0',
    CURRENT_DATE,
    0,
    ARRAY[]::uuid[],
    now(),
    'scheduled'
)
ON CONFLICT DO NOTHING;

-- ============================================================
-- Evidence freshness across >=2 classes
-- ============================================================
-- The seeded control declares freshness_class = 'monthly'. Insert a
-- freshness row for it (monthly, stale=false) plus a second freshness
-- row with class='weekly' on a synthetic second control.
INSERT INTO controls (
    id, tenant_id, scf_id, title, description, control_family,
    implementation_type, owner_role, lifecycle_state, applicability_expr,
    bundle_id, freshness_class
)
VALUES (
    '33333333-3333-3333-3333-333333330002',
    '00000000-0000-0000-0000-00000000d3a0',
    'AST-01',
    'Asset inventory weekly review',
    'Weekly review of the asset inventory completeness.',
    'Asset Management',
    'manual_periodic',
    'security-engineering',
    'active',
    'env == "prod"',
    'demo-asset-inventory',
    'weekly'
)
ON CONFLICT DO NOTHING;

INSERT INTO evidence_freshness (
    id, tenant_id, control_id, freshness_class, latest_observed_at,
    valid_until, is_stale, evidence_count, refreshed_at
)
VALUES
(
    '99999999-9999-9999-9999-999999990001',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330001',
    'monthly',
    now() - INTERVAL '5 days',
    now() + INTERVAL '25 days',
    FALSE,
    3,
    now()
),
(
    '99999999-9999-9999-9999-999999990002',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330002',
    'weekly',
    now() - INTERVAL '10 days',
    now() - INTERVAL '3 days',
    TRUE,
    1,
    now()
)
ON CONFLICT DO NOTHING;

-- ============================================================
-- Exception expiring within 30 days (upcoming panel)
-- ============================================================
INSERT INTO exceptions (
    id, tenant_id, control_id, scope_cell_predicate, justification,
    compensating_controls, requested_by, requested_at, approved_by,
    approved_at, expires_at, status
)
VALUES (
    'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaa0001',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330001',
    '{}'::jsonb,
    'Migration to managed KMS pending; staging cluster runs without SSE until 2026-06.',
    ARRAY['Weekly manual bucket audit'],
    'demo-operator@example.invalid',
    now() - INTERVAL '60 days',
    'demo-approver@example.invalid',
    now() - INTERVAL '59 days',
    now() + INTERVAL '14 days',
    'approved'
)
ON CONFLICT DO NOTHING;

COMMIT;
