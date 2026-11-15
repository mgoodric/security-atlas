-- #1800 — preconditions for web/e2e/org-mapping.spec.ts: overlay edges
-- shown as "org mapping" in the web UI.
--
-- A synthetic framework version (slug `om-e2e`, current) with one
-- requirement OM-01, and two SCF anchors in the current SCF release:
--
--   OM-01 -> OM-E2E-A1   catalog edge (global, every tenant sees it)
--   OM-01 -> OM-E2E-A2   overlay edge, demo tenant only, with a rationale
--                        and a source
--
-- Control C2 in the demo tenant is anchored on A2, so its coverage reaches
-- OM-01 only through the overlay edge.
--
-- A second tenant (`Org-mapping e2e tenant B`) gets a tenants row and
-- nothing else. The spec signs in as that tenant and checks the overlay
-- edge is invisible: fw_to_scf_overlay_edges is under FORCE RLS, and
-- effective_fw_to_scf_edges filters the overlay arm by tenant too (#1798).
--
-- Every INSERT is ON CONFLICT DO NOTHING, so a re-run is a no-op. The
-- overlay row is inserted directly rather than through POST
-- /v1/mapping-overlays: its created_by is then a fixed value the spec can
-- assert, and the create API's own behaviour is covered by
-- internal/api/mappingoverlays' integration tests.

\set ON_ERROR_STOP on

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES
    ('00000000-0000-0000-0000-00000000d3a0', 'Demo Tenant', FALSE),
    ('e2e18000-0000-0000-0000-00000000bbbb', 'Org-mapping e2e tenant B', FALSE)
ON CONFLICT (id) DO NOTHING;

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
VALUES (
    'e2e18000-0000-0000-0000-0000000f0001',
    NULL,
    'om-e2e',
    'Org mapping e2e framework',
    'e2e',
    '#1800 e2e synthetic framework for the org-mapping spec.'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO framework_versions (id, tenant_id, framework_id, version, effective_from, status)
VALUES
    ('e2e18000-0000-0000-0000-0000000f0003', NULL,
     'e2e18000-0000-0000-0000-0000000f0001', '2026-om-e2e', '2026-01-01', 'current')
ON CONFLICT (id) DO NOTHING;

UPDATE frameworks
   SET latest_version_id = 'e2e18000-0000-0000-0000-0000000f0003'
 WHERE id = 'e2e18000-0000-0000-0000-0000000f0001';

-- The anchors live in the current SCF release: requirement coverage reads
-- pin to it by default (#2203), so an anchor in any other release is not
-- read. The CI e2e database imports no SCF catalog, so when no release is
-- current this seeds the same synthetic one controls-list.sql does (same
-- ids, so the two fixtures converge in either order). The anchors then go
-- in the release scfrelease.Lookup picks: the current one, ordered as
-- ListFrameworkVersionsBySlug orders it.
INSERT INTO frameworks (id, tenant_id, name, slug, issuer, description)
SELECT 'e2e74300-0000-0000-0000-0000000f0001', NULL, 'Secure Controls Framework', 'scf',
       'Secure Controls Framework Council',
       'Slice 743 e2e synthetic SCF spine — current version for the controls-list spec.'
WHERE NOT EXISTS (
    SELECT 1 FROM framework_versions fv JOIN frameworks f ON f.id = fv.framework_id
    WHERE f.slug = 'scf' AND f.tenant_id IS NULL AND fv.tenant_id IS NULL AND fv.status = 'current')
ON CONFLICT (id) DO NOTHING;

INSERT INTO framework_versions (id, tenant_id, framework_id, version, effective_from, status, oscal_catalog_uri)
SELECT 'e2e74300-0000-0000-0000-0000000f0002', NULL, 'e2e74300-0000-0000-0000-0000000f0001',
       '2026.1-e2e', '2026-01-01', 'current', 'urn:scf:e2e:2026.1'
WHERE NOT EXISTS (
    SELECT 1 FROM framework_versions fv JOIN frameworks f ON f.id = fv.framework_id
    WHERE f.slug = 'scf' AND f.tenant_id IS NULL AND fv.tenant_id IS NULL AND fv.status = 'current')
ON CONFLICT (id) DO NOTHING;

INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
SELECT a.id::uuid, cur.id, a.scf_id, 'HRS', a.title, ''
FROM (VALUES
    ('e2e18000-0000-0000-0000-00000000a001', 'OM-E2E-A1', 'Org mapping e2e catalog anchor'),
    ('e2e18000-0000-0000-0000-00000000a002', 'OM-E2E-A2', 'Org mapping e2e overlay anchor')
) AS a (id, scf_id, title)
CROSS JOIN (
    SELECT fv.id FROM framework_versions fv JOIN frameworks f ON f.id = fv.framework_id
    WHERE f.slug = 'scf' AND fv.tenant_id IS NULL AND fv.status = 'current'
    ORDER BY fv.effective_from DESC NULLS LAST, fv.version DESC
    LIMIT 1
) AS cur
ON CONFLICT (id) DO NOTHING;

INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
VALUES
    ('e2e18000-0000-0000-0000-00000000b001', 'e2e18000-0000-0000-0000-0000000f0003',
     'OM-01', 'Confidentiality agreements', '')
ON CONFLICT (id) DO NOTHING;

INSERT INTO fw_to_scf_edges
    (id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution, rationale)
VALUES
    ('e2e18000-0000-0000-0000-00000000e001', 'e2e18000-0000-0000-0000-00000000b001',
     'e2e18000-0000-0000-0000-00000000a001', 'equal', 1.0, 'community_draft', '')
ON CONFLICT (id) DO NOTHING;

INSERT INTO fw_to_scf_overlay_edges
    (id, tenant_id, framework_requirement_id, scf_anchor_id, relationship_type, strength,
     rationale, source_ref, created_by, created_at)
VALUES
    ('e2e18000-0000-0000-0000-00000000e002', '00000000-0000-0000-0000-00000000d3a0',
     'e2e18000-0000-0000-0000-00000000b001', 'e2e18000-0000-0000-0000-00000000a002',
     'intersects_with', 0.8,
     'Auditor tested the NDA control under OM-01',
     'FY25 SOC 2 report, section 4, OM-01',
     'org-mapping-e2e-author', '2026-11-01T12:00:00Z')
ON CONFLICT (id) DO NOTHING;

INSERT INTO controls (
    id, tenant_id, bundle_id, version, scf_id, scf_anchor_id, title, description,
    control_family, implementation_type, owner_role, lifecycle_state, applicability_expr,
    evidence_queries, manual_evidence_schema, linked_policy_ids,
    freshness_class, bundle_manifest_yaml, bundle_manifest_hash,
    bundle_uploaded_at, bundle_uploaded_by
)
VALUES
    ('e2e18000-0000-0000-0000-00000000c002', '00000000-0000-0000-0000-00000000d3a0',
     'om_e2e_c2', 1, 'OM-E2E-A2', 'e2e18000-0000-0000-0000-00000000a002',
     'Org mapping e2e NDA control', '', 'access_control', 'automated', 'security', 'active', '',
     '[]'::jsonb, NULL, ARRAY[]::TEXT[], NULL, '', '', now(), 'e2e')
ON CONFLICT (id) DO NOTHING;

COMMIT;
