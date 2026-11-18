-- #2141 — Playwright e2e seed for `web/e2e/controls-bulk-2141.spec.ts`.
--
-- A tenant of the spec's own, so its bulk writes (assign owner, evaluate
-- now, link to a plan) never race the controls-list spec, whose tests run
-- in parallel over the demo tenant's three fixture controls. It holds:
--   - an admin (the signed-in approver) and a second member, the owner
--     the bulk assign hands the controls to;
--   - three active controls;
--   - one action plan the bulk link targets.
-- Owner assignments and the plan's control links are reset each run, so
-- a rerun on the shared Postgres starts from the same state. No
-- feature_flags rows: every module reads its seed default (Risk and
-- Exceptions on).
--
-- Tenant UUID matches TENANT in the spec; the `2141` suffix carries the
-- ticket. Every id and string is neutral (P0-A9 / GitGuardian).

\set ON_ERROR_STOP on

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-000000002141', 'Controls bulk (e2e)', false)
ON CONFLICT DO NOTHING;

BEGIN;
SET LOCAL app.current_tenant = '00000000-0000-0000-0000-000000002141';

-- users.id is global, not per tenant: the second member is ...2141b2, not
-- ...2142, which is theme.sql's (#2142) user. Sharing it moved that user
-- into this tenant and its PATCH /api/me answered 404.
INSERT INTO users (id, tenant_id, email, display_name, status, idp_issuer, idp_subject)
VALUES
    ('44444444-4444-4444-4444-444444442141', '00000000-0000-0000-0000-000000002141',
     'controls-bulk-admin-e2e@example.invalid', 'Bulk Admin E2E', 'active', '', ''),
    ('44444444-4444-4444-4444-4444442141b2', '00000000-0000-0000-0000-000000002141',
     'controls-bulk-owner-e2e@example.invalid', 'Bulk Owner E2E', 'active', '', '')
ON CONFLICT (id) DO UPDATE SET status = 'active';

INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES ('00000000-0000-0000-0000-000000002141', '44444444-4444-4444-4444-444444442141', 'admin', 'e2e:controls-bulk'),
       ('00000000-0000-0000-0000-000000002141', '44444444-4444-4444-4444-4444442141b2', 'grc_engineer', 'e2e:controls-bulk')
ON CONFLICT DO NOTHING;

INSERT INTO controls (id, tenant_id, title, control_family, implementation_type,
                      owner_role, lifecycle_state, bundle_id, freshness_class,
                      evidence_queries, applicability_expr)
VALUES
    ('88888888-8888-8888-8888-888888882141', '00000000-0000-0000-0000-000000002141',
     'Bulk e2e control one', 'IAC', 'automated', 'security-engineering', 'active',
     'bundle-e2e-2141-1', 'monthly', '[]'::jsonb, 'true'),
    ('88888888-8888-8888-8888-888888882142', '00000000-0000-0000-0000-000000002141',
     'Bulk e2e control two', 'IAC', 'automated', 'security-engineering', 'active',
     'bundle-e2e-2141-2', 'monthly', '[]'::jsonb, 'true'),
    ('88888888-8888-8888-8888-888888882143', '00000000-0000-0000-0000-000000002141',
     'Bulk e2e control three', 'IAC', 'automated', 'security-engineering', 'active',
     'bundle-e2e-2141-3', 'monthly', '[]'::jsonb, 'true')
ON CONFLICT (id) DO NOTHING;

INSERT INTO action_plans (id, tenant_id, title, owner_id, due_date, status)
VALUES (
    'acacacac-acac-acac-acac-acacacac2141',
    '00000000-0000-0000-0000-000000002141',
    'Bulk e2e remediation plan',
    '44444444-4444-4444-4444-444444442141',
    (now() + INTERVAL '30 days')::date,
    'in_progress'
)
ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status;

-- Determinism reset: NOT on-conflict; scoped to this tenant's rows.
DELETE FROM action_plan_controls
 WHERE tenant_id = '00000000-0000-0000-0000-000000002141'
   AND action_plan_id = 'acacacac-acac-acac-acac-acacacac2141';

DELETE FROM control_owner_assignments
 WHERE tenant_id = '00000000-0000-0000-0000-000000002141';

COMMIT;
