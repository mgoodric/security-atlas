-- #2136 — preconditions for web/e2e/posture-targets.spec.ts, posture
-- targets on the dashboard's framework-posture tiles.
--
-- One synthetic catalog framework with two current versions, each label
-- unique so the spec can find its tile:
--
--   2136-scoped-e2e    two requirements, one covered by a demo-tenant
--                      control (coverage 50%), and an ACTIVATED framework
--                      scope for the demo tenant, so the tile shows a
--                      percentage.
--   2136-unscoped-e2e  one requirement and no scope: the tile says
--                      "Scope not set".
--
-- Reset: the spec sets a target on the scoped version, so each run first
-- deletes the demo tenant's target for it. framework_posture_target_audit_log
-- is append-only and its rows stay, as they would for any change. Every
-- other INSERT is ON CONFLICT DO NOTHING.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
VALUES (
    'e2e21360-0000-0000-0000-0000000f0001',
    NULL,
    'posture-targets-e2e',
    'Posture targets (e2e)',
    'e2e',
    '#2136 e2e synthetic framework for the posture-target tile spec.'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO framework_versions (id, tenant_id, framework_id, version, effective_from, status)
VALUES
    ('e2e21360-0000-0000-0000-0000000f0002', NULL,
     'e2e21360-0000-0000-0000-0000000f0001', '2136-scoped-e2e', '2026-01-01', 'current'),
    ('e2e21360-0000-0000-0000-0000000f0003', NULL,
     'e2e21360-0000-0000-0000-0000000f0001', '2136-unscoped-e2e', '2026-01-01', 'current')
ON CONFLICT (id) DO NOTHING;

INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
VALUES
    ('e2e21360-0000-0000-0000-00000000b001', 'e2e21360-0000-0000-0000-0000000f0002', 'PT-01', 'Posture target requirement one', ''),
    ('e2e21360-0000-0000-0000-00000000b002', 'e2e21360-0000-0000-0000-0000000f0002', 'PT-02', 'Posture target requirement two', ''),
    ('e2e21360-0000-0000-0000-00000000b003', 'e2e21360-0000-0000-0000-0000000f0003', 'PT-01', 'Posture target requirement one', '')
ON CONFLICT (id) DO NOTHING;

-- PT-01 of the scoped version is covered: an edge to anchor A1 on a legacy
-- spine version, and a demo-tenant control anchored there. PT-02 is not,
-- so the tile reads 1/2 = 50.0%.
INSERT INTO framework_versions (id, tenant_id, framework_id, version, effective_from, status)
VALUES ('e2e21360-0000-0000-0000-0000000f0004', NULL,
        'e2e21360-0000-0000-0000-0000000f0001', 'scf-2136-e2e', '2026-01-01', 'legacy')
ON CONFLICT (id) DO NOTHING;

INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
VALUES ('e2e21360-0000-0000-0000-00000000a001', 'e2e21360-0000-0000-0000-0000000f0004',
        'PT-E2E-A1', 'IAC', 'Posture target e2e anchor A1', '')
ON CONFLICT (id) DO NOTHING;

INSERT INTO fw_to_scf_edges
    (id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution, rationale)
VALUES ('e2e21360-0000-0000-0000-00000000e001', 'e2e21360-0000-0000-0000-00000000b001',
        'e2e21360-0000-0000-0000-00000000a001', 'equal', 1.0, 'community_draft', '')
ON CONFLICT (id) DO NOTHING;

INSERT INTO controls (
    id, tenant_id, bundle_id, version, scf_id, scf_anchor_id, title, description,
    control_family, implementation_type, owner_role, lifecycle_state, applicability_expr,
    evidence_queries, manual_evidence_schema, linked_policy_ids,
    freshness_class, bundle_manifest_yaml, bundle_manifest_hash,
    bundle_uploaded_at, bundle_uploaded_by
)
VALUES ('e2e21360-0000-0000-0000-00000000d001', '00000000-0000-0000-0000-00000000d3a0',
        'posture_2136_e2e_c1', 1, 'PT-E2E-A1', 'e2e21360-0000-0000-0000-00000000a001',
        'Posture target e2e control', '', 'access_control', 'automated', 'security', 'active', '',
        '[]'::jsonb, NULL, ARRAY[]::TEXT[], NULL, '', '', now(), 'e2e')
ON CONFLICT (id) DO NOTHING;

INSERT INTO framework_scopes
    (id, tenant_id, framework_version_id, name, predicate, predicate_hash, state)
VALUES (
    'e2e21360-0000-0000-0000-00000000c001',
    '00000000-0000-0000-0000-00000000d3a0',
    'e2e21360-0000-0000-0000-0000000f0002',
    'Posture targets e2e scope',
    '{"op":"true"}'::jsonb,
    'e2e-2136',
    'draft'
)
ON CONFLICT (id) DO NOTHING;

UPDATE framework_scopes
   SET state = 'activated', effective_from = COALESCE(effective_from, now())
 WHERE id = 'e2e21360-0000-0000-0000-00000000c001'
   AND state <> 'activated';

DELETE FROM framework_posture_targets
 WHERE tenant_id = '00000000-0000-0000-0000-00000000d3a0'
   AND framework_version_id = 'e2e21360-0000-0000-0000-0000000f0002';

COMMIT;
