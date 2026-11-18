-- #2174 — Playwright e2e seed for `web/e2e/module-links.spec.ts`.
--
-- A tenant of the spec's own with one control, one policy linked to it,
-- and two active grc_engineer members: one whose module access is every
-- optional module except Policies, one with every optional module except
-- Risk and Exceptions (#2140). Both reset each run. No feature_flags
-- rows: every module reads its seed default.
--
-- Tenant UUID matches TENANT in the spec; the `2174` suffix carries the
-- ticket.

\set ON_ERROR_STOP on

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-000000002174', 'Module links (e2e)', false)
ON CONFLICT DO NOTHING;

BEGIN;
SET LOCAL app.current_tenant = '00000000-0000-0000-0000-000000002174';
INSERT INTO users (id, tenant_id, email, display_name, status, idp_issuer, idp_subject)
VALUES (
    '44444444-4444-4444-4444-444444442174',
    '00000000-0000-0000-0000-000000002174',
    'module-links-member-e2e@example.invalid',
    'Module Links Member E2E',
    'active', '', ''
)
ON CONFLICT (id) DO UPDATE SET status = 'active';
INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES ('00000000-0000-0000-0000-000000002174', '44444444-4444-4444-4444-444444442174', 'grc_engineer', 'e2e:module-links')
ON CONFLICT DO NOTHING;
INSERT INTO member_module_access (tenant_id, user_id, modules, updated_by)
VALUES ('00000000-0000-0000-0000-000000002174', '44444444-4444-4444-4444-444444442174',
        ARRAY['risk', 'third_party_risk', 'questionnaires', 'personnel_security', 'exceptions', 'board_reporting'],
        'e2e:module-links')
ON CONFLICT (tenant_id, user_id) DO UPDATE SET modules = EXCLUDED.modules;

-- #2140: a second grc_engineer member with every optional module except
-- Risk (which owns action plans) and Exceptions, so the control page's
-- Linked section leaves out the action plans and exceptions parts.
INSERT INTO users (id, tenant_id, email, display_name, status, idp_issuer, idp_subject)
VALUES (
    '44444444-4444-4444-4444-444444442140',
    '00000000-0000-0000-0000-000000002174',
    'module-links-member-b-e2e@example.invalid',
    'Module Links Member B E2E',
    'active', '', ''
)
ON CONFLICT (id) DO UPDATE SET status = 'active';
INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES ('00000000-0000-0000-0000-000000002174', '44444444-4444-4444-4444-444444442140', 'grc_engineer', 'e2e:module-links')
ON CONFLICT DO NOTHING;
INSERT INTO member_module_access (tenant_id, user_id, modules, updated_by)
VALUES ('00000000-0000-0000-0000-000000002174', '44444444-4444-4444-4444-444444442140',
        ARRAY['third_party_risk', 'policies', 'questionnaires', 'personnel_security', 'board_reporting'],
        'e2e:module-links')
ON CONFLICT (tenant_id, user_id) DO UPDATE SET modules = EXCLUDED.modules;

-- #2141: active, so the controls list shows it for the bulk-action
-- module checks.
INSERT INTO controls (id, tenant_id, title, control_family, implementation_type,
                      bundle_id, freshness_class, evidence_queries, applicability_expr,
                      lifecycle_state)
VALUES ('88888888-8888-8888-8888-888888882174', '00000000-0000-0000-0000-000000002174',
        'E2E 2174 control', 'HRS', 'automated', 'bundle-e2e-2174', 'monthly', '[]'::jsonb, 'true',
        'active')
ON CONFLICT (id) DO UPDATE SET lifecycle_state = EXCLUDED.lifecycle_state;

INSERT INTO policies (
    id, tenant_id, title, version, status, body_md, owner_role, approver_role,
    created_by, linked_control_ids
)
VALUES (
    '55555555-5555-5555-5555-555555552174', '00000000-0000-0000-0000-000000002174',
    'E2E 2174 linked policy', '1.0.0', 'draft', '# body', 'security_lead', 'ciso',
    'e2e:module-links', ARRAY['88888888-8888-8888-8888-888888882174']::uuid[]
)
ON CONFLICT (id) DO UPDATE SET linked_control_ids = EXCLUDED.linked_control_ids;
COMMIT;
