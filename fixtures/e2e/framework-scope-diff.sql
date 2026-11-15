-- #1795 — preconditions for web/e2e/framework-scope-diff.spec.ts, the
-- applicability diff on the framework-scope review screen.
--
-- A synthetic framework version (`2026-diff-e2e`) with four requirements
-- DIFF-01..DIFF-04 and no crosswalk: the spec reads decisions, not
-- coverage. The spec creates both scope revisions itself through the API
-- (the predicate hash is the platform's to compute).
--
-- Reset: the spec activates revisions on this version, so a re-run must
-- start with no scope on it. The DELETE below removes every scope revision
-- of the version (their decisions cascade); framework_scope_workflow_log is
-- append-only and keeps its rows. Every other INSERT is ON CONFLICT DO
-- NOTHING.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

-- #2101: the e2e user both submits and approves each revision, which needs
-- the organisation's Allow self-approval setting. Turned on for the demo
-- tenant and left on (framework-scope-soa.sql does the same; the specs run
-- in parallel). The tenants row is inserted as controls-top-bar.sql does.
INSERT INTO tenants (id, name, is_bootstrap_tenant, allow_self_approval)
VALUES ('00000000-0000-0000-0000-00000000d3a0', 'Demo Tenant', FALSE, TRUE)
ON CONFLICT (id) DO UPDATE SET allow_self_approval = TRUE;

INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
VALUES (
    'e2e17950-0000-0000-0000-0000000f0001',
    NULL,
    'scope-diff-e2e',
    'Scope diff framework (e2e)',
    'e2e',
    '#1795 e2e synthetic framework for the review-screen applicability diff.'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO framework_versions (id, tenant_id, framework_id, version, effective_from, status)
VALUES
    ('e2e17950-0000-0000-0000-0000000f0002', NULL,
     'e2e17950-0000-0000-0000-0000000f0001', '2026-diff-e2e', '2026-01-01', 'current')
ON CONFLICT (id) DO NOTHING;

UPDATE frameworks
   SET latest_version_id = 'e2e17950-0000-0000-0000-0000000f0002'
 WHERE id = 'e2e17950-0000-0000-0000-0000000f0001';

INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
VALUES
    ('e2e17950-0000-0000-0000-00000000b001', 'e2e17950-0000-0000-0000-0000000f0002', 'DIFF-01', 'Asset inventory', ''),
    ('e2e17950-0000-0000-0000-00000000b002', 'e2e17950-0000-0000-0000-0000000f0002', 'DIFF-02', 'Cardholder data retention', ''),
    ('e2e17950-0000-0000-0000-00000000b003', 'e2e17950-0000-0000-0000-0000000f0002', 'DIFF-03', 'Change management', ''),
    ('e2e17950-0000-0000-0000-00000000b004', 'e2e17950-0000-0000-0000-0000000f0002', 'DIFF-04', 'Physical entry controls', '')
ON CONFLICT (id) DO NOTHING;

-- NOT ON CONFLICT: the reset that makes a re-run start with no scope.
DELETE FROM framework_scopes
 WHERE tenant_id = '00000000-0000-0000-0000-00000000d3a0'
   AND framework_version_id = 'e2e17950-0000-0000-0000-0000000f0002';

COMMIT;
