-- Slice 082 — Playwright e2e seed for `web/e2e/control-detail.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). Other specs also load this fixture (action-plans-verified-2109,
-- control-detail-tabs, controls-anchor-fit, enum-labels) and read only the
-- 00-seed demo control in the demo tenant; nothing below touches the demo
-- tenant.
--
-- #2755 — the control-detail spec's own tenant (UUID suffix 2755b01…),
-- holding one control laid out for every assertion the spec makes:
--
--   - control `cd_e2e_c1` anchored to SCF anchor CD-E2E-A1, applicability
--     `true`, in a tenant with one scope cell (environment=prod);
--   - two tenant-scoped frameworks, each with a current version and one
--     requirement crosswalked to the anchor:
--       Alpha  CD-ALPHA-1  equal      strength 0.90  activated scope
--                                                    {"op":"true"}: in scope
--       Beta   CD-BETA-1   subset_of  strength 0.60  activated scope on
--                                                    environment=dev, which
--                                                    matches no cell: out of
--                                                    scope (dashed row, n/a)
--   - one `pass` evaluation, so 30-day effectiveness has data and the
--     in-scope row's coverage is numeric (strength x pass rate);
--   - no evidence, policies, risks or exceptions: those sections render
--     their honest empty states.
--
-- Reset: the evaluation is re-stamped at now() each run, so it stays inside
-- the 30-day effectiveness window and is the control's only state row.
-- Every other INSERT is ON CONFLICT DO NOTHING.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-2755b0100001';

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-2755b0100001', 'Control detail (e2e)', false)
ON CONFLICT DO NOTHING;

INSERT INTO scope_cells (id, tenant_id, label, dimensions, dimensions_hash)
VALUES ('00000000-0000-0000-0000-2755b0100002',
        '00000000-0000-0000-0000-2755b0100001',
        'e2e 2755 prod', '{"environment":"prod"}'::jsonb, 'e2e-2755b01-prod')
ON CONFLICT DO NOTHING;

INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
VALUES
    ('00000000-0000-0000-0000-2755b0100a01', '00000000-0000-0000-0000-2755b0100001',
     'cd-e2e-alpha', 'Control Detail Alpha', 'Acme Industries',
     '#2755 e2e framework; the control is in its scope.'),
    ('00000000-0000-0000-0000-2755b0100b01', '00000000-0000-0000-0000-2755b0100001',
     'cd-e2e-beta', 'Control Detail Beta', 'Acme Industries',
     '#2755 e2e framework; the control is out of its scope.')
ON CONFLICT DO NOTHING;

INSERT INTO framework_versions (id, tenant_id, framework_id, version, effective_from, status)
VALUES
    ('00000000-0000-0000-0000-2755b0100a02', '00000000-0000-0000-0000-2755b0100001',
     '00000000-0000-0000-0000-2755b0100a01', '2026', '2026-01-01', 'current'),
    ('00000000-0000-0000-0000-2755b0100b02', '00000000-0000-0000-0000-2755b0100001',
     '00000000-0000-0000-0000-2755b0100b01', '2026', '2026-01-01', 'current')
ON CONFLICT DO NOTHING;

INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
VALUES
    ('00000000-0000-0000-0000-2755b0100a03', '00000000-0000-0000-0000-2755b0100a02',
     'CD-ALPHA-1', 'Encrypt stored data', ''),
    ('00000000-0000-0000-0000-2755b0100b03', '00000000-0000-0000-0000-2755b0100b02',
     'CD-BETA-1', 'Protect stored data', '')
ON CONFLICT DO NOTHING;

-- scf_anchors has no tenant. Hanging the anchor off the tenant-scoped Alpha
-- version (not the current SCF version) keeps it out of every /controls
-- list, which reads only the current SCF version's anchors.
INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
VALUES ('00000000-0000-0000-0000-2755b0100c01', '00000000-0000-0000-0000-2755b0100a02',
        'CD-E2E-A1', 'Cryptography', 'Control detail e2e anchor', '')
ON CONFLICT DO NOTHING;

INSERT INTO fw_to_scf_edges
    (id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution, rationale)
VALUES
    ('00000000-0000-0000-0000-2755b0100a04', '00000000-0000-0000-0000-2755b0100a03',
     '00000000-0000-0000-0000-2755b0100c01', 'equal', 0.90, 'community_draft', ''),
    ('00000000-0000-0000-0000-2755b0100b04', '00000000-0000-0000-0000-2755b0100b03',
     '00000000-0000-0000-0000-2755b0100c01', 'subset_of', 0.60, 'community_draft', '')
ON CONFLICT DO NOTHING;

INSERT INTO controls (
    id, tenant_id, bundle_id, version, scf_id, scf_anchor_id, title, description,
    control_family, implementation_type, owner_role, lifecycle_state, applicability_expr,
    freshness_class
)
VALUES ('00000000-0000-0000-0000-2755b0100c02', '00000000-0000-0000-0000-2755b0100001',
        'cd_e2e_c1', 1, 'CD-E2E-A1', '00000000-0000-0000-0000-2755b0100c01',
        'Control detail e2e control', '', 'Cryptography', 'automated',
        'security-engineering', 'active', 'true', 'monthly')
ON CONFLICT DO NOTHING;

INSERT INTO framework_scopes
    (id, tenant_id, framework_version_id, name, predicate, predicate_hash, state)
VALUES
    ('00000000-0000-0000-0000-2755b0100a05', '00000000-0000-0000-0000-2755b0100001',
     '00000000-0000-0000-0000-2755b0100a02', 'Alpha e2e scope',
     '{"op":"true"}'::jsonb, 'e2e-2755b01-alpha', 'activated'),
    ('00000000-0000-0000-0000-2755b0100b05', '00000000-0000-0000-0000-2755b0100001',
     '00000000-0000-0000-0000-2755b0100b02', 'Beta e2e scope',
     '{"op":"eq","dim":"environment","value":"dev"}'::jsonb, 'e2e-2755b01-beta', 'activated')
ON CONFLICT DO NOTHING;

-- The evaluation reset described in the header: drop any other evaluation
-- of the control (the recompute sweep writes them) and upsert the seeded
-- one at now(). An upsert, not delete-then-insert, because each Playwright
-- worker runs this fixture in its own beforeAll, concurrently.
DELETE FROM control_evaluations
 WHERE tenant_id = '00000000-0000-0000-0000-2755b0100001'
   AND control_id = '00000000-0000-0000-0000-2755b0100c02'
   AND id <> '00000000-0000-0000-0000-2755b0100c03';

INSERT INTO control_evaluations (
    id, tenant_id, control_id, scope_cell_id, eval_run_id, evaluated_at,
    result, freshness_status, evidence_count_in_window, last_observed_at,
    freshness_class, trigger
)
VALUES ('00000000-0000-0000-0000-2755b0100c03', '00000000-0000-0000-0000-2755b0100001',
        '00000000-0000-0000-0000-2755b0100c02', NULL,
        '00000000-0000-0000-0000-2755b0100c04', now(),
        'pass', 'fresh', 1, now() - interval '1 hour', 'monthly', 'manual')
ON CONFLICT (id) DO UPDATE
   SET evaluated_at = EXCLUDED.evaluated_at,
       last_observed_at = EXCLUDED.last_observed_at;

COMMIT;
