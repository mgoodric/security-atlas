-- #2755 — Playwright e2e seed for `web/e2e/calendar.spec.ts`.
--
-- A tenant of the spec's own with one event of each type the spec
-- checks, all inside the calendar's default 90-day window:
--
--   * audit     — an open audit period ending in 20 days
--   * exception — an active exception expiring in 40 days
--   * policy    — a published policy whose next review is in 30 days
--   * vendor    — a monthly-cadence vendor last reviewed 20 days ago,
--                 so its next review is due in about 10 days
--   * control   — a never-evaluated manual_attested control, whose review
--                 the calendar places at "now" (today in the month grid)
--
-- Every date is relative to now() and refreshed on each run (ON CONFLICT
-- DO UPDATE) so the events stay inside the window whatever day the suite
-- runs. Tenant UUID matches TENANT in the spec.

\set ON_ERROR_STOP on

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-2755a0000031', 'Calendar (e2e)', false)
ON CONFLICT DO NOTHING;

BEGIN;
SET LOCAL app.current_tenant = '00000000-0000-0000-0000-2755a0000031';

INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
VALUES (
    '11111111-1111-1111-1111-2755a0000301',
    '00000000-0000-0000-0000-2755a0000031',
    'e2e-calendar-fwk',
    'Calendar Framework',
    'Acme Industries',
    'Synthetic framework for the #2755 calendar e2e spec.'
)
ON CONFLICT DO NOTHING;

INSERT INTO framework_versions (id, tenant_id, framework_id, version, effective_from)
VALUES (
    '11111111-1111-1111-1111-2755a0000302',
    '00000000-0000-0000-0000-2755a0000031',
    '11111111-1111-1111-1111-2755a0000301',
    '1.0',
    '2026-01-01'
)
ON CONFLICT DO NOTHING;

INSERT INTO audit_periods (
    id, tenant_id, name, framework_version_id, period_start, period_end,
    status, created_by
)
VALUES (
    'cccccccc-cccc-cccc-cccc-2755a0000311',
    '00000000-0000-0000-0000-2755a0000031',
    'E2E calendar period',
    '11111111-1111-1111-1111-2755a0000302',
    (now() - interval '60 days')::date, (now() + interval '20 days')::date,
    'open', 'e2e:calendar'
)
ON CONFLICT (id) DO UPDATE SET
    period_start = EXCLUDED.period_start,
    period_end = EXCLUDED.period_end;

INSERT INTO controls (
    id, tenant_id, title, description, control_family,
    implementation_type, owner_role, lifecycle_state, applicability_expr,
    bundle_id, freshness_class
)
VALUES (
    '33333333-3333-3333-3333-2755a0000311',
    '00000000-0000-0000-0000-2755a0000031',
    'E2E calendar control',
    'A manual control the calendar e2e spec reviews.',
    'Identity & Access Management',
    'manual_attested',
    'security-engineering',
    'active',
    '{"op":"true"}',
    'e2e-calendar-control',
    'quarterly'
)
ON CONFLICT DO NOTHING;

INSERT INTO exceptions (
    id, tenant_id, control_id, scope_cell_predicate, justification,
    compensating_controls, requested_by, requested_at, approved_by,
    approved_at, activated_by, activated_at, effective_from, expires_at,
    status
)
VALUES (
    'eeeeeeee-eeee-eeee-eeee-2755a0000311',
    '00000000-0000-0000-0000-2755a0000031',
    '33333333-3333-3333-3333-2755a0000311',
    '{}'::jsonb,
    'Calendar e2e exception.',
    ARRAY[]::text[],
    'e2e:calendar-requester',
    now(),
    'e2e:calendar-approver',
    now(),
    'e2e:calendar-approver',
    now(),
    now(),
    now() + interval '40 days',
    'active'
)
ON CONFLICT (id) DO UPDATE SET
    requested_at = EXCLUDED.requested_at,
    expires_at = EXCLUDED.expires_at;

INSERT INTO policies (
    id, tenant_id, title, version, status, effective_date,
    body_md, owner_role, approver_role, created_by,
    published_at, published_by, next_review_at
)
VALUES (
    '55555555-5555-5555-5555-2755a0000311',
    '00000000-0000-0000-0000-2755a0000031',
    'E2E calendar policy', '1.0.0', 'published', now()::date,
    '# body', 'security_lead', 'ciso', 'e2e:calendar',
    now(), 'e2e:calendar', now() + interval '30 days'
)
ON CONFLICT (id) DO UPDATE SET next_review_at = EXCLUDED.next_review_at;

INSERT INTO vendors (id, tenant_id, name, criticality, review_cadence, last_review_date)
VALUES (
    '77777777-7777-7777-7777-2755a0000311',
    '00000000-0000-0000-0000-2755a0000031',
    'E2E calendar vendor', 'high', 'monthly', (now() - interval '20 days')::date
)
ON CONFLICT (id) DO UPDATE SET last_review_date = EXCLUDED.last_review_date;
COMMIT;
