-- #2116 — preconditions for web/e2e/policy-lifecycle.spec.ts, the policy
-- workflow in the app against the real platform.
--
-- A second user, the approver: an admin in the demo tenant, signed in by
-- the spec with a JWT minted by /v1/test/issue-jwt. The e2e user creates
-- and submits; this user approves and publishes, so the approval is not
-- self-approved. The policy links the base seed's control (00-seed.sql).
--
-- No reset: the spec titles each run's policies uniquely, so a re-run
-- never meets a policy an earlier run left. Every INSERT is idempotent.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

INSERT INTO users (
    id, tenant_id, email, display_name, status, idp_issuer, idp_subject
)
VALUES (
    '44444444-4444-4444-4444-444444442116',
    '00000000-0000-0000-0000-00000000d3a0',
    'policy-approver-e2e@example.invalid',
    'Policy Approver E2E',
    'active',
    '',
    ''
)
ON CONFLICT (id) DO UPDATE SET status = 'active';

INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES (
    '00000000-0000-0000-0000-00000000d3a0',
    '44444444-4444-4444-4444-444444442116',
    'admin',
    'e2e:policy-lifecycle'
)
ON CONFLICT DO NOTHING;

COMMIT;
