-- #1794 — preconditions for web/e2e/framework-scope-soa.spec.ts, the
-- Statement of Applicability tab on the framework-scope page.
--
-- A synthetic ISO/IEC 27001 version (slug `iso27001`, so the tab is titled
-- "Statement of Applicability") with eight requirements SOA-01..SOA-08,
-- crosswalked to three SCF anchors on a legacy spine version:
--
--   SOA-01 -> A1  control C1, latest evaluation pass
--   SOA-02 -> A2  control C2, latest evaluation pass
--   SOA-03 -> A3  control C3, latest evaluation fail
--   SOA-04..08    no edge
--
-- Dashboard coverage for the version is therefore 3/8 = 37.5% with no
-- activated scope. The spec creates the draft scope itself through the API
-- (the predicate hash is the platform's to compute) and excludes SOA-01,
-- which C1 passes behind, as the contradiction.
--
-- Reset: the spec walks the scope to `activated`, which changes dashboard
-- coverage, so a re-run must start with no scope on this version. The
-- DELETE below removes every scope revision of the version (their
-- decisions cascade). framework_scope_workflow_log has no FK and is
-- append-only; its rows for the deleted revisions stay, as they would for
-- any deleted scope.
--
-- The three controls' evaluations are reset too: the atlas server's
-- recompute sweep (hourly, and once at startup) writes an `inconclusive`
-- row for a control with no evidence, which would become the latest
-- evaluation and clear the contradiction. Deleting the controls'
-- evaluations and inserting fresh ones makes the seeded results the
-- latest on every run. Every other INSERT is ON CONFLICT DO NOTHING.
--
-- The version label `2022-soa-e2e` is unique so the spec can find this
-- version's tile on the dashboard framework-posture panel.
--
-- #2101: the e2e user both submits and approves the scope, which needs the
-- organisation's Allow self-approval setting. It is turned on for the demo
-- tenant here, and left on: framework-scope-diff.sql does the same, and the
-- two specs run in parallel, so neither may turn it off under the other.
-- The tenants row is inserted as controls-top-bar.sql does.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

INSERT INTO tenants (id, name, is_bootstrap_tenant, allow_self_approval)
VALUES ('00000000-0000-0000-0000-00000000d3a0', 'Demo Tenant', FALSE, TRUE)
ON CONFLICT (id) DO UPDATE SET allow_self_approval = TRUE;

INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
VALUES (
    'e2e17940-0000-0000-0000-0000000f0001',
    NULL,
    'iso27001',
    'ISO/IEC 27001 (e2e)',
    'e2e',
    '#1794 e2e synthetic ISO/IEC 27001 version for the SoA tab spec.'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO framework_versions (id, tenant_id, framework_id, version, effective_from, status)
VALUES
    ('e2e17940-0000-0000-0000-0000000f0002', NULL,
     'e2e17940-0000-0000-0000-0000000f0001', 'scf-soa-e2e', '2026-01-01', 'legacy'),
    ('e2e17940-0000-0000-0000-0000000f0003', NULL,
     'e2e17940-0000-0000-0000-0000000f0001', '2022-soa-e2e', '2026-01-01', 'current')
ON CONFLICT (id) DO NOTHING;

UPDATE frameworks
   SET latest_version_id = 'e2e17940-0000-0000-0000-0000000f0003'
 WHERE id = 'e2e17940-0000-0000-0000-0000000f0001';

INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
VALUES
    ('e2e17940-0000-0000-0000-00000000a001', 'e2e17940-0000-0000-0000-0000000f0002',
     'SOA-E2E-A1', 'IAC', 'SoA e2e anchor A1', ''),
    ('e2e17940-0000-0000-0000-00000000a002', 'e2e17940-0000-0000-0000-0000000f0002',
     'SOA-E2E-A2', 'IAC', 'SoA e2e anchor A2', ''),
    ('e2e17940-0000-0000-0000-00000000a003', 'e2e17940-0000-0000-0000-0000000f0002',
     'SOA-E2E-A3', 'IAC', 'SoA e2e anchor A3', '')
ON CONFLICT (id) DO NOTHING;

INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
VALUES
    ('e2e17940-0000-0000-0000-00000000b001', 'e2e17940-0000-0000-0000-0000000f0003', 'SOA-01', 'Access control policy', ''),
    ('e2e17940-0000-0000-0000-00000000b002', 'e2e17940-0000-0000-0000-0000000f0003', 'SOA-02', 'Identity management', ''),
    ('e2e17940-0000-0000-0000-00000000b003', 'e2e17940-0000-0000-0000-0000000f0003', 'SOA-03', 'Authentication information', ''),
    ('e2e17940-0000-0000-0000-00000000b004', 'e2e17940-0000-0000-0000-0000000f0003', 'SOA-04', 'Access rights', ''),
    ('e2e17940-0000-0000-0000-00000000b005', 'e2e17940-0000-0000-0000-0000000f0003', 'SOA-05', 'Information security in supplier relationships', ''),
    ('e2e17940-0000-0000-0000-00000000b006', 'e2e17940-0000-0000-0000-0000000f0003', 'SOA-06', 'Secure development life cycle', ''),
    ('e2e17940-0000-0000-0000-00000000b007', 'e2e17940-0000-0000-0000-0000000f0003', 'SOA-07', 'Outsourced development', ''),
    ('e2e17940-0000-0000-0000-00000000b008', 'e2e17940-0000-0000-0000-0000000f0003', 'SOA-08', 'Physical security monitoring', '')
ON CONFLICT (id) DO NOTHING;

INSERT INTO fw_to_scf_edges
    (id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution, rationale)
VALUES
    ('e2e17940-0000-0000-0000-00000000e001', 'e2e17940-0000-0000-0000-00000000b001',
     'e2e17940-0000-0000-0000-00000000a001', 'equal', 1.0, 'community_draft', ''),
    ('e2e17940-0000-0000-0000-00000000e002', 'e2e17940-0000-0000-0000-00000000b002',
     'e2e17940-0000-0000-0000-00000000a002', 'equal', 1.0, 'community_draft', ''),
    ('e2e17940-0000-0000-0000-00000000e003', 'e2e17940-0000-0000-0000-00000000b003',
     'e2e17940-0000-0000-0000-00000000a003', 'equal', 1.0, 'community_draft', '')
ON CONFLICT (id) DO NOTHING;

INSERT INTO controls (
    id, tenant_id, bundle_id, version, scf_id, scf_anchor_id, title, description,
    control_family, implementation_type, owner_role, lifecycle_state, applicability_expr,
    evidence_queries, manual_evidence_schema, linked_policy_ids,
    freshness_class, bundle_manifest_yaml, bundle_manifest_hash,
    bundle_uploaded_at, bundle_uploaded_by
)
VALUES
    ('e2e17940-0000-0000-0000-00000000c001', '00000000-0000-0000-0000-00000000d3a0',
     'soa_e2e_c1', 1, 'SOA-E2E-A1', 'e2e17940-0000-0000-0000-00000000a001',
     'SoA e2e control C1', '', 'access_control', 'automated', 'security', 'active', '',
     '[]'::jsonb, NULL, ARRAY[]::TEXT[], NULL, '', '', now(), 'e2e'),
    ('e2e17940-0000-0000-0000-00000000c002', '00000000-0000-0000-0000-00000000d3a0',
     'soa_e2e_c2', 1, 'SOA-E2E-A2', 'e2e17940-0000-0000-0000-00000000a002',
     'SoA e2e control C2', '', 'access_control', 'automated', 'security', 'active', '',
     '[]'::jsonb, NULL, ARRAY[]::TEXT[], NULL, '', '', now(), 'e2e'),
    ('e2e17940-0000-0000-0000-00000000c003', '00000000-0000-0000-0000-00000000d3a0',
     'soa_e2e_c3', 1, 'SOA-E2E-A3', 'e2e17940-0000-0000-0000-00000000a003',
     'SoA e2e control C3', '', 'access_control', 'automated', 'security', 'active', '',
     '[]'::jsonb, NULL, ARRAY[]::TEXT[], NULL, '', '', now(), 'e2e')
ON CONFLICT (id) DO NOTHING;

-- NOT ON CONFLICT: the evaluation reset described in the header.
DELETE FROM control_evaluations
 WHERE tenant_id = '00000000-0000-0000-0000-00000000d3a0'
   AND control_id IN ('e2e17940-0000-0000-0000-00000000c001',
                      'e2e17940-0000-0000-0000-00000000c002',
                      'e2e17940-0000-0000-0000-00000000c003');

INSERT INTO control_evaluations (
    id, tenant_id, control_id, scope_cell_id, eval_run_id,
    evaluated_at, result, freshness_status, evidence_count_in_window, trigger
)
VALUES
    ('e2e17940-0000-0000-0000-0000000ee001', '00000000-0000-0000-0000-00000000d3a0',
     'e2e17940-0000-0000-0000-00000000c001', NULL, 'e2e17940-0000-0000-0000-0000000ff001',
     now(), 'pass', 'fresh', 1, 'manual'),
    ('e2e17940-0000-0000-0000-0000000ee002', '00000000-0000-0000-0000-00000000d3a0',
     'e2e17940-0000-0000-0000-00000000c002', NULL, 'e2e17940-0000-0000-0000-0000000ff001',
     now(), 'pass', 'fresh', 1, 'manual'),
    ('e2e17940-0000-0000-0000-0000000ee003', '00000000-0000-0000-0000-00000000d3a0',
     'e2e17940-0000-0000-0000-00000000c003', NULL, 'e2e17940-0000-0000-0000-0000000ff001',
     now(), 'fail', 'fresh', 1, 'manual')
ON CONFLICT (id) DO NOTHING;

-- NOT ON CONFLICT: the reset that makes a re-run start with no scope.
DELETE FROM framework_scopes
 WHERE tenant_id = '00000000-0000-0000-0000-00000000d3a0'
   AND framework_version_id = 'e2e17940-0000-0000-0000-0000000f0003';

COMMIT;
