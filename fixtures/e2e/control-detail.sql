-- Slice 082 / 112 — Playwright e2e seed for
-- `web/e2e/control-detail.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The control-detail spec's preconditions (per its preamble):
--
--   - TEST_BEARER carries a credential in a tenant with at least one
--     control anchored to an SCF anchor with >=2 framework requirement
--     mappings
--   - at least one of those frameworks has an activated FrameworkScope
--     the control is OUT of (so the dashed/greyed OOS row has data)
--   - KNOWN_CONTROL_ID is that control's UUID
--
-- Inserts are idempotent. Rows whose exact state matters to the enabled
-- assertions use ON CONFLICT ... DO UPDATE.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

-- The 00-seed.sql control (33333333-3333-3333-3333-333333330001) is
-- the KNOWN_CONTROL_ID for this spec. Slice 112 upgrades that control
-- into the FULL control-detail fixture shape.

-- ============================================================
-- Scope cells for real applicability / FrameworkScope intersection
-- ============================================================
INSERT INTO scope_dimensions (
    id, tenant_id, name, value_type, allowed_values, is_required, is_builtin
)
VALUES
(
    'e2e11200-0000-0000-0000-000000000101',
    '00000000-0000-0000-0000-00000000d3a0',
    'business_unit',
    'string',
    '[]'::jsonb,
    FALSE,
    TRUE
),
(
    'e2e11200-0000-0000-0000-000000000102',
    '00000000-0000-0000-0000-00000000d3a0',
    'environment',
    'string',
    '["prod","staging","dev","sandbox"]'::jsonb,
    TRUE,
    TRUE
),
(
    'e2e11200-0000-0000-0000-000000000103',
    '00000000-0000-0000-0000-00000000d3a0',
    'data_classification',
    'string',
    '["restricted","confidential","internal","public"]'::jsonb,
    TRUE,
    TRUE
)
ON CONFLICT (tenant_id, name) DO NOTHING;

INSERT INTO scope_cells (id, tenant_id, label, dimensions, dimensions_hash)
VALUES
(
    'e2e11200-0000-0000-0000-000000000201',
    '00000000-0000-0000-0000-00000000d3a0',
    'control-detail-prod-internal',
    '{"business_unit":"default","data_classification":"internal","environment":"prod"}'::jsonb,
    '8b8e4c4bbdb5f45d8ecee7bb68508c8ebcdd3012508e2b8f303eab8951829506'
),
(
    'e2e11200-0000-0000-0000-000000000202',
    '00000000-0000-0000-0000-00000000d3a0',
    'control-detail-staging-confidential',
    '{"business_unit":"platform","data_classification":"confidential","environment":"staging"}'::jsonb,
    '84bf595b592a9b8c2d16480a7ee7543ab1f659b95271edbdfb1610c3f2fa7bac'
)
ON CONFLICT (tenant_id, dimensions_hash) DO NOTHING;

INSERT INTO scopes (
    id, tenant_id, business_unit, environment, geography, cloud_account,
    data_classification, product_line
)
VALUES (
    'e2e11200-0000-0000-0000-000000000202',
    '00000000-0000-0000-0000-00000000d3a0',
    'platform',
    'staging',
    'us-east-1',
    'demo-aws-123456789012',
    'confidential',
    'acme-saas-core'
)
ON CONFLICT DO NOTHING;

-- ============================================================
-- Multi-framework graph: one in-scope row and one out-of-scope row
-- ============================================================
INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
VALUES (
    'e2e11200-0000-0000-0000-00000000f101',
    NULL,
    'control-detail-in',
    'Control Detail In-Scope Framework',
    'Acme Industries',
    'Synthetic global framework used only by the control-detail e2e fixture.'
)
ON CONFLICT DO NOTHING;

INSERT INTO framework_versions (
    id, tenant_id, framework_id, version, effective_from, status,
    requirement_count, oscal_catalog_uri
)
VALUES (
    'e2e11200-0000-0000-0000-00000000f102',
    NULL,
    'e2e11200-0000-0000-0000-00000000f101',
    '1.0',
    '2026-01-01',
    'current',
    1,
    'urn:e2e:control-detail:in-scope:1.0'
)
ON CONFLICT DO NOTHING;

INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
VALUES (
    'e2e11200-0000-0000-0000-00000000f001',
    NULL,
    'control-detail-oos',
    'Control Detail OOS Framework',
    'Acme Industries',
    'Synthetic global framework used only by the control-detail e2e fixture.'
)
ON CONFLICT (id) DO UPDATE
SET tenant_id = NULL,
    description = EXCLUDED.description;

INSERT INTO framework_versions (
    id, tenant_id, framework_id, version, effective_from, status,
    requirement_count, oscal_catalog_uri
)
VALUES (
    'e2e11200-0000-0000-0000-00000000f002',
    NULL,
    'e2e11200-0000-0000-0000-00000000f001',
    '1.0',
    '2026-01-01',
    'current',
    1,
    'urn:e2e:control-detail:oos:1.0'
)
ON CONFLICT (id) DO UPDATE
SET tenant_id = NULL,
    status = EXCLUDED.status,
    requirement_count = EXCLUDED.requirement_count;

INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
VALUES
(
    'e2e11200-0000-0000-0000-00000000b003',
    'e2e11200-0000-0000-0000-00000000f102',
    'CD-INSCOPE-1',
    'Control detail in-scope requirement',
    'Synthetic in-scope requirement for the control-detail Playwright spec.'
),
(
    'e2e11200-0000-0000-0000-00000000b002',
    'e2e11200-0000-0000-0000-00000000f002',
    'CD-OOS-1',
    'Control detail out-of-scope requirement',
    'Synthetic out-of-scope requirement for the control-detail Playwright spec.'
)
ON CONFLICT DO NOTHING;

INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
VALUES (
    'e2e11200-0000-0000-0000-00000000a001',
    'e2e11200-0000-0000-0000-00000000f102',
    '0E2E112-1',
    'Cryptography',
    'Control detail multi-anchor fixture',
    'Synthetic SCF anchor seeded by slice 112 for the control-detail e2e spec.'
)
ON CONFLICT (id) DO UPDATE
SET framework_version_id = EXCLUDED.framework_version_id,
    scf_id = EXCLUDED.scf_id,
    family = EXCLUDED.family,
    title = EXCLUDED.title,
    description = EXCLUDED.description,
    updated_at = now();

INSERT INTO fw_to_scf_edges (
    id, framework_requirement_id, scf_anchor_id, relationship_type,
    strength, source_attribution, rationale
)
VALUES
(
    'e2e11200-0000-0000-0000-00000000e003',
    'e2e11200-0000-0000-0000-00000000b003',
    'e2e11200-0000-0000-0000-00000000a001',
    'equal',
    0.90,
    'org_internal',
    'Synthetic in-scope edge for control-detail e2e coverage.'
),
(
    'e2e11200-0000-0000-0000-00000000e002',
    'e2e11200-0000-0000-0000-00000000b002',
    'e2e11200-0000-0000-0000-00000000a001',
    'subset_of',
    0.70,
    'org_internal',
    'Synthetic out-of-scope edge for control-detail e2e coverage.'
)
ON CONFLICT DO NOTHING;

UPDATE controls
SET scf_id = '0E2E112-1',
    scf_anchor_id = 'e2e11200-0000-0000-0000-00000000a001',
    applicability_expr = '{"dim":"environment","op":"eq","value":"prod"}',
    bundle_id = 'e2e-112-control-detail',
    freshness_class = 'monthly',
    updated_at = now()
WHERE tenant_id = '00000000-0000-0000-0000-00000000d3a0'
  AND id = '33333333-3333-3333-3333-333333330001';

INSERT INTO framework_scopes (
    id, tenant_id, framework_version_id, name, state, predicate,
    predicate_hash, approver_user_id, approved_at, predicate_hash_at_approval,
    effective_from
)
VALUES (
    'e2e11200-0000-0000-0000-00000000f101',
    '00000000-0000-0000-0000-00000000d3a0',
    'e2e11200-0000-0000-0000-00000000f102',
    'control-detail in-scope framework',
    'activated',
    '{"dim":"environment","op":"eq","value":"prod"}'::jsonb,
    'cb5da16aecf7257186c56d399920142d66c2c854a591948ad3627dc6cab23365',
    '44444444-4444-4444-4444-444444440001',
    now() - INTERVAL '1 day',
    'cb5da16aecf7257186c56d399920142d66c2c854a591948ad3627dc6cab23365',
    now() - INTERVAL '1 day'
)
ON CONFLICT (id) DO UPDATE
SET framework_version_id = EXCLUDED.framework_version_id,
    state = EXCLUDED.state,
    predicate = EXCLUDED.predicate,
    predicate_hash = EXCLUDED.predicate_hash,
    approver_user_id = EXCLUDED.approver_user_id,
    approved_at = EXCLUDED.approved_at,
    predicate_hash_at_approval = EXCLUDED.predicate_hash_at_approval,
    effective_from = EXCLUDED.effective_from,
    updated_at = now();

-- ============================================================
-- Evidence, freshness, evaluation history, and drift
-- ============================================================
INSERT INTO evidence_records (
    id, tenant_id, control_id, control_ref, scope_id, observed_at, ingested_at,
    provenance, result, payload, hash, freshness_class, valid_until
)
VALUES
(
    'e2e11200-0000-0000-0000-000000000301',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330001',
    '33333333-3333-3333-3333-333333330001',
    '22222222-2222-2222-2222-222222220001',
    now() - INTERVAL '2 days',
    now() - INTERVAL '2 days',
    '{"source":"control-detail-e2e","scope":"in-scope"}'::jsonb,
    'pass',
    '{"encrypted":true}'::jsonb,
    'e2e112-control-detail-in-scope',
    'monthly',
    now() + INTERVAL '28 days'
),
(
    'e2e11200-0000-0000-0000-000000000302',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330001',
    '33333333-3333-3333-3333-333333330001',
    'e2e11200-0000-0000-0000-000000000202',
    now() - INTERVAL '1 day',
    now() - INTERVAL '1 day',
    '{"source":"control-detail-e2e","scope":"out-of-scope"}'::jsonb,
    'pass',
    '{"encrypted":true}'::jsonb,
    'e2e112-control-detail-out-of-scope',
    'monthly',
    now() + INTERVAL '29 days'
)
ON CONFLICT DO NOTHING;

INSERT INTO control_evaluations (
    id, tenant_id, control_id, scope_cell_id, eval_run_id, evaluated_at,
    result, freshness_status, evidence_count_in_window, last_observed_at,
    freshness_class, trigger
)
VALUES
(
    'e2e11200-0000-0000-0000-000000000401',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330001',
    'e2e11200-0000-0000-0000-000000000201',
    'e2e11200-0000-0000-0000-000000000499',
    now() - INTERVAL '2 days',
    'pass',
    'fresh',
    1,
    now() - INTERVAL '2 days',
    'monthly',
    'manual'
),
(
    'e2e11200-0000-0000-0000-000000000402',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330001',
    'e2e11200-0000-0000-0000-000000000201',
    'e2e11200-0000-0000-0000-000000000499',
    now() - INTERVAL '1 day',
    'pass',
    'fresh',
    1,
    now() - INTERVAL '1 day',
    'monthly',
    'manual'
)
ON CONFLICT DO NOTHING;

INSERT INTO evidence_freshness (
    id, tenant_id, control_id, freshness_class, latest_observed_at,
    valid_until, is_stale, evidence_count, refreshed_at
)
VALUES (
    'e2e11200-0000-0000-0000-000000000501',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330001',
    'monthly',
    now() - INTERVAL '1 day',
    now() + INTERVAL '29 days',
    FALSE,
    2,
    now()
)
ON CONFLICT (tenant_id, control_id) DO UPDATE
SET freshness_class = EXCLUDED.freshness_class,
    latest_observed_at = EXCLUDED.latest_observed_at,
    valid_until = EXCLUDED.valid_until,
    is_stale = EXCLUDED.is_stale,
    evidence_count = EXCLUDED.evidence_count,
    refreshed_at = EXCLUDED.refreshed_at;

INSERT INTO control_drift_snapshots (
    id, tenant_id, snapshot_date, controls_passing, passing_control_ids,
    captured_at, trigger
)
VALUES (
    'e2e11200-0000-0000-0000-000000000601',
    '00000000-0000-0000-0000-00000000d3a0',
    CURRENT_DATE,
    1,
    ARRAY['33333333-3333-3333-3333-333333330001'::uuid],
    now(),
    'manual'
)
ON CONFLICT DO NOTHING;

COMMIT;
