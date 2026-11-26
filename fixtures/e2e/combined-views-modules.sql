-- #2173 — Playwright e2e seed for `web/e2e/combined-views-modules.spec.ts`.
--
-- A tenant of the spec's own with one active grc_engineer member whose
-- module access is Policies only, and three reviews due in five days, one
-- per module: a policy review (Policies), a risk review (Risk) and a
-- monthly vendor review (Third-party risk). Dates are refreshed each run
-- so the three stay inside the dashboard's 14-day Upcoming window. No
-- feature_flags rows: every module reads its seed default (all three on).
--
-- Tenant UUID matches TENANT in the spec; the `2173` suffix carries the
-- ticket.

\set ON_ERROR_STOP on

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-000000002173', 'Combined views (e2e)', false)
ON CONFLICT DO NOTHING;

BEGIN;
SET LOCAL app.current_tenant = '00000000-0000-0000-0000-000000002173';
INSERT INTO users (id, tenant_id, email, display_name, status, idp_issuer, idp_subject)
VALUES (
    '44444444-4444-4444-4444-444444442173',
    '00000000-0000-0000-0000-000000002173',
    'combined-views-member-e2e@example.invalid',
    'Combined Views Member E2E',
    'active', '', ''
)
ON CONFLICT (id) DO UPDATE SET status = 'active';
INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES ('00000000-0000-0000-0000-000000002173', '44444444-4444-4444-4444-444444442173', 'grc_engineer', 'e2e:combined-views')
ON CONFLICT DO NOTHING;
INSERT INTO member_module_access (tenant_id, user_id, modules, updated_by)
VALUES ('00000000-0000-0000-0000-000000002173', '44444444-4444-4444-4444-444444442173', ARRAY['policies'], 'e2e:combined-views')
ON CONFLICT (tenant_id, user_id) DO UPDATE SET modules = EXCLUDED.modules;

INSERT INTO policies (
    id, tenant_id, title, version, status, effective_date,
    body_md, owner_role, approver_role, created_by,
    published_at, published_by, next_review_at
)
VALUES (
    '55555555-5555-5555-5555-555555552173', '00000000-0000-0000-0000-000000002173',
    'E2E 2173 policy', '1.0.0', 'published', now()::date,
    '# body', 'security_lead', 'ciso', 'e2e:combined-views',
    now(), 'e2e:combined-views', now() + interval '5 days'
)
ON CONFLICT (id) DO UPDATE SET next_review_at = EXCLUDED.next_review_at;

INSERT INTO risks (
    id, tenant_id, title, category_id, treatment, treatment_owner,
    inherent_score, residual_score, review_due_at
)
VALUES (
    '66666666-6666-6666-6666-666666662173', '00000000-0000-0000-0000-000000002173',
    'E2E 2173 risk', risk_category_id('00000000-0000-0000-0000-000000002173', 'operational'),
    'mitigate'::risk_treatment, '44444444-4444-4444-4444-444444442173',
    '{"likelihood": 3, "impact": 4}'::jsonb, '{"likelihood": 2, "impact": 3}'::jsonb,
    now() + interval '5 days'
)
ON CONFLICT (id) DO UPDATE SET review_due_at = EXCLUDED.review_due_at;

INSERT INTO vendors (id, tenant_id, name, criticality, review_cadence, last_review_date)
VALUES (
    '77777777-7777-7777-7777-777777772173', '00000000-0000-0000-0000-000000002173',
    'E2E 2173 vendor', 'high', 'monthly', (now() - interval '25 days')::date
)
ON CONFLICT (id) DO UPDATE SET last_review_date = EXCLUDED.last_review_date;
COMMIT;
