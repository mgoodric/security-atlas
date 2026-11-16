-- #2115 — preconditions for web/e2e/framework-scope-wizard.spec.ts, the
-- framework-scope wizard against the real platform.
--
-- One synthetic catalog framework with one current version,
-- `2115-wizard-e2e`, labelled uniquely so the spec finds its tile and its
-- picker option. It has two requirements, one covered by a demo-tenant
-- control (the posture-targets.sql shape), so once a scope is activated
-- the tile reads 1/2 = 50.0%.
--
-- A second user, the approver: an admin in the demo tenant, signed in by
-- the spec with a JWT minted by /v1/test/issue-jwt. The e2e user submits
-- and this user approves, so the scope is not self-approved.
--
-- Reset: the spec creates and activates a scope on the version, so each
-- run first deletes every scope revision of it (their decisions cascade;
-- framework_scope_workflow_log is append-only and keeps its rows). Every
-- other INSERT is ON CONFLICT DO NOTHING.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
VALUES (
    'e2e21150-0000-0000-0000-0000000f0001',
    NULL,
    'scope-wizard-e2e',
    'Scope wizard framework (e2e)',
    'e2e',
    '#2115 e2e synthetic framework for the framework-scope wizard spec.'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO framework_versions (id, tenant_id, framework_id, version, effective_from, status)
VALUES
    ('e2e21150-0000-0000-0000-0000000f0002', NULL,
     'e2e21150-0000-0000-0000-0000000f0001', '2115-wizard-e2e', '2026-01-01', 'current')
ON CONFLICT (id) DO NOTHING;

UPDATE frameworks
   SET latest_version_id = 'e2e21150-0000-0000-0000-0000000f0002'
 WHERE id = 'e2e21150-0000-0000-0000-0000000f0001';

INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
VALUES
    ('e2e21150-0000-0000-0000-00000000b001', 'e2e21150-0000-0000-0000-0000000f0002', 'WIZ-01', 'Scope wizard requirement one', ''),
    ('e2e21150-0000-0000-0000-00000000b002', 'e2e21150-0000-0000-0000-0000000f0002', 'WIZ-02', 'Scope wizard requirement two', '')
ON CONFLICT (id) DO NOTHING;

-- WIZ-01 is covered: an edge to anchor A1 on a legacy spine version, and a
-- demo-tenant control anchored there. WIZ-02 is not.
INSERT INTO framework_versions (id, tenant_id, framework_id, version, effective_from, status)
VALUES ('e2e21150-0000-0000-0000-0000000f0004', NULL,
        'e2e21150-0000-0000-0000-0000000f0001', 'scf-2115-e2e', '2026-01-01', 'legacy')
ON CONFLICT (id) DO NOTHING;

INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
VALUES ('e2e21150-0000-0000-0000-00000000a001', 'e2e21150-0000-0000-0000-0000000f0004',
        'WIZ-E2E-A1', 'IAC', 'Scope wizard e2e anchor A1', '')
ON CONFLICT (id) DO NOTHING;

INSERT INTO fw_to_scf_edges
    (id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution, rationale)
VALUES ('e2e21150-0000-0000-0000-00000000e001', 'e2e21150-0000-0000-0000-00000000b001',
        'e2e21150-0000-0000-0000-00000000a001', 'equal', 1.0, 'community_draft', '')
ON CONFLICT (id) DO NOTHING;

INSERT INTO controls (
    id, tenant_id, bundle_id, version, scf_id, scf_anchor_id, title, description,
    control_family, implementation_type, owner_role, lifecycle_state, applicability_expr,
    evidence_queries, manual_evidence_schema, linked_policy_ids,
    freshness_class, bundle_manifest_yaml, bundle_manifest_hash,
    bundle_uploaded_at, bundle_uploaded_by
)
VALUES ('e2e21150-0000-0000-0000-00000000d001', '00000000-0000-0000-0000-00000000d3a0',
        'scope_wizard_2115_e2e_c1', 1, 'WIZ-E2E-A1', 'e2e21150-0000-0000-0000-00000000a001',
        'Scope wizard e2e control', '', 'access_control', 'automated', 'security', 'active', '',
        '[]'::jsonb, NULL, ARRAY[]::TEXT[], NULL, '', '', now(), 'e2e')
ON CONFLICT (id) DO NOTHING;

INSERT INTO users (
    id, tenant_id, email, display_name, status, idp_issuer, idp_subject
)
VALUES (
    '44444444-4444-4444-4444-444444442115',
    '00000000-0000-0000-0000-00000000d3a0',
    'scope-approver-e2e@example.invalid',
    'Scope Approver E2E',
    'active',
    '',
    ''
)
ON CONFLICT (id) DO UPDATE SET status = 'active';

INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES (
    '00000000-0000-0000-0000-00000000d3a0',
    '44444444-4444-4444-4444-444444442115',
    'admin',
    'e2e:framework-scope-wizard'
)
ON CONFLICT DO NOTHING;

-- NOT ON CONFLICT: the reset that makes a re-run start with no scope.
DELETE FROM framework_scopes
 WHERE tenant_id = '00000000-0000-0000-0000-00000000d3a0'
   AND framework_version_id = 'e2e21150-0000-0000-0000-0000000f0002';

COMMIT;
