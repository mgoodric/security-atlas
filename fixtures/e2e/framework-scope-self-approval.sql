-- #2433 — Playwright e2e seed for
-- `web/e2e/framework-scope-self-approval.spec.ts`: the approval rule
-- against the real platform, in a tenant with Allow self-approval off.
--
-- A tenant of the spec's own (the demo tenant leaves self-approval on for
-- the seeded scope specs), with two active tenant admins: the requester,
-- who submits a scope and is refused when approving it, and a second
-- person, who approves and activates it. One synthetic catalog framework
-- version, labelled uniquely, to scope.
--
-- Every id is the spec's own, `2433` suffix: `users.id` is global, so a
-- user id shared with another fixture would move that user between
-- tenants (#2617).
--
-- Reset: the spec creates and activates a scope on the version, so each
-- run first deletes every scope revision of it (their decisions cascade;
-- framework_scope_workflow_log is append-only and keeps its rows), and
-- turns self-approval off again. Every other INSERT is ON CONFLICT.

\set ON_ERROR_STOP on

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-000000002433', 'Scope self-approval off (e2e)', false)
ON CONFLICT DO NOTHING;

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-000000002433';

-- The default, set again in case a run left it on (tenants is FORCE RLS,
-- hence inside the tenant context).
UPDATE tenants
   SET allow_self_approval = false
 WHERE id = '00000000-0000-0000-0000-000000002433';

INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
VALUES (
    'e2e24330-0000-0000-0000-0000000f0001',
    NULL,
    'scope-self-approval-e2e',
    'Scope self-approval framework (e2e)',
    'e2e',
    '#2433 e2e synthetic framework for the framework-scope self-approval spec.'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO framework_versions (id, tenant_id, framework_id, version, effective_from, status)
VALUES
    ('e2e24330-0000-0000-0000-0000000f0002', NULL,
     'e2e24330-0000-0000-0000-0000000f0001', '2433-self-approval-e2e', '2026-01-01', 'current')
ON CONFLICT (id) DO NOTHING;

UPDATE frameworks
   SET latest_version_id = 'e2e24330-0000-0000-0000-0000000f0002'
 WHERE id = 'e2e24330-0000-0000-0000-0000000f0001';

INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
VALUES
    ('e2e24330-0000-0000-0000-00000000b001', 'e2e24330-0000-0000-0000-0000000f0002',
     'SELF-01', 'Scope self-approval requirement', '')
ON CONFLICT (id) DO NOTHING;

INSERT INTO users (id, tenant_id, email, display_name, status, idp_issuer, idp_subject)
VALUES
    ('44444444-4444-4444-4444-444444442433',
     '00000000-0000-0000-0000-000000002433',
     'scope-requester-2433-e2e@example.invalid',
     'Scope Requester E2E',
     'active', '', ''),
    ('44444444-4444-4444-4444-444444442443',
     '00000000-0000-0000-0000-000000002433',
     'scope-second-approver-2433-e2e@example.invalid',
     'Scope Second Approver E2E',
     'active', '', '')
ON CONFLICT (id) DO UPDATE SET status = 'active';

DELETE FROM user_roles
 WHERE tenant_id = '00000000-0000-0000-0000-000000002433';
INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES
    ('00000000-0000-0000-0000-000000002433', '44444444-4444-4444-4444-444444442433', 'admin', 'e2e:framework-scope-self-approval'),
    ('00000000-0000-0000-0000-000000002433', '44444444-4444-4444-4444-444444442443', 'admin', 'e2e:framework-scope-self-approval');

-- NOT ON CONFLICT: the reset that makes a re-run start with no scope.
DELETE FROM framework_scopes
 WHERE tenant_id = '00000000-0000-0000-0000-000000002433'
   AND framework_version_id = 'e2e24330-0000-0000-0000-0000000f0002';

COMMIT;
