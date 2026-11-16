-- Slice 082 — Playwright e2e seed for `web/e2e/dashboard.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The dashboard spec's preconditions (per its preamble):
--
--   - at least one risk with treatment=mitigate (top-risks panel)
--   - at least one control that has drifted out of passing in the last
--     7 days (recent-drift panel)
--   - evidence records across >=2 freshness classes (freshness panel)
--   - an exception expiring in 10 days, a risk review due in 7 and an
--     action plan due in 5 (the "Coming up" panel reads the compliance
--     calendar's next 14 days, #2130)
--
-- Most inserts are ON CONFLICT DO NOTHING for idempotency across re-runs.
-- The exceptions row is `ON CONFLICT (id) DO UPDATE` per slice 193: the
-- `status` column is read by the calendar (`status='active'` filter); a
-- prior run's stale value must be overwritten to guarantee the seed
-- state on every re-run. The risk review date and the action plan are
-- reset on every run for the same reason.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

-- ============================================================
-- A risk with treatment=mitigate (linked to the seeded control)
-- ============================================================
-- The risk carries its category reference, looked up by slug in this
-- tenant's own categories (#1816); the risk_category enum is gone (#1819).
-- seed_risk_categories is idempotent.
SELECT seed_risk_categories('00000000-0000-0000-0000-00000000d3a0');

INSERT INTO risks (
    id, tenant_id, title, description, category_id, treatment,
    treatment_owner, inherent_score, residual_score
)
VALUES (
    '77777777-7777-7777-7777-777777770001',
    '00000000-0000-0000-0000-00000000d3a0',
    'Unencrypted production data at rest',
    'Risk that production S3 buckets in customer-data accounts ship without server-side encryption.',
    (SELECT id FROM org_risk_categories
     WHERE tenant_id = '00000000-0000-0000-0000-00000000d3a0' AND slug = 'confidentiality'),
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
-- Exception expiring in 10 days ("Coming up" panel)
-- ============================================================
-- Slice 193 — status MUST be 'active' (not 'approved'). The calendar's
-- exception branch (`ListCalendarEvents` in
-- internal/db/queries/calendar.sql) filters `e.status = 'active'`;
-- so do the slice-040 `/v1/exceptions/expiring` query and every
-- integration test seed. #2130: 10 days, not 14, so the expiry sits
-- inside the panel's [today, today + 14d) window whatever the hour. The pre-slice-193 fixture seeded 'approved'
-- — that never showed because slice 082's assertions were commented
-- out. Slice 111 un-skipped, AC-5 broke; slice 193 fixes it.
-- ON CONFLICT (id) DO UPDATE follows the slice-168 settings.sql
-- precedent: DO NOTHING would leave a stale row from a prior local
-- run untouched, defeating the re-seed. Update both status and
-- expires_at so re-runs always start from a known-good "active +
-- expires in 10d" state. activated_by/activated_at are set so the
-- exception's lifecycle narrative is consistent (requested →
-- approved → active) and the `exceptions_sod` CHECK constraint
-- (activator MUST differ from requester) is satisfied — we reuse
-- demo-approver as the activator (a distinct identity from
-- demo-operator the requester).
INSERT INTO exceptions (
    id, tenant_id, control_id, scope_cell_predicate, justification,
    compensating_controls, requested_by, requested_at, approved_by,
    approved_at, activated_by, activated_at, expires_at, status
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
    'demo-approver@example.invalid',
    now() - INTERVAL '58 days',
    now() + INTERVAL '10 days',
    'active'
)
ON CONFLICT (id) DO UPDATE
SET status = EXCLUDED.status,
    expires_at = EXCLUDED.expires_at,
    activated_by = EXCLUDED.activated_by,
    activated_at = EXCLUDED.activated_at;

-- ============================================================
-- #2130: a risk review and an action plan due inside 14 days
-- ============================================================
-- The mitigate risk above gets its review date moved into the window on
-- every run (the risk insert is DO NOTHING, and #2059's trigger set the
-- original date a year out).
UPDATE risks
SET review_due_at = now() + INTERVAL '7 days'
WHERE id = '77777777-7777-7777-7777-777777770001';

-- An open action plan due in 5 days, owned by the shared e2e user
-- (the same id settings.sql / controls-list.sql seed; DO NOTHING keeps
-- whichever fixture created it first).
INSERT INTO users (
    id, tenant_id, email, display_name, status, idp_issuer, idp_subject
)
VALUES (
    '44444444-4444-4444-4444-444444440001',
    '00000000-0000-0000-0000-00000000d3a0',
    'dashboard-e2e-user@example.invalid',
    'Dashboard E2E Operator',
    'active',
    '',
    ''
)
ON CONFLICT DO NOTHING;

INSERT INTO action_plans (id, tenant_id, title, owner_id, due_date, status)
VALUES (
    'acacacac-acac-acac-acac-acacacac0001',
    '00000000-0000-0000-0000-00000000d3a0',
    'Enable SSE on staging buckets',
    '44444444-4444-4444-4444-444444440001',
    (now() + INTERVAL '5 days')::date,
    'in_progress'
)
ON CONFLICT (id) DO UPDATE
SET due_date = EXCLUDED.due_date,
    tombstoned_at = NULL;

COMMIT;
