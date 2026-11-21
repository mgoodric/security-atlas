-- #2755 — Playwright e2e seed for `web/e2e/evidence-list.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The spec asserts on row counts and filter narrowing, so it
-- cannot share the demo tenant (other fixtures add evidence there). It
-- signs in to a tenant of its own, seeded here with:
--
--   * control ...2755c000a001 — six evidence records, one per result
--     value plus two older passes, across two evidence kinds;
--   * control ...2755c000a002 — no evidence (drives the empty state);
--   * control ...2755c000a003 — one evidence record, 45 days old, so
--     picking ...a001 in the Control pill has a row to drop (#2794) and
--     "All time" has a row outside the 30-day default window;
--   * one SCF anchor behind each of ...a001 and ...a003, so the Control
--     pill, which lists anchors that have a tenant control (#2771), has
--     two options;
--   * one scope cell, so the Scope pill has an option to pick.
--
-- Evidence is an append-only ledger (canvas §4.3), so this file only
-- ever inserts, keyed on fixed ids with ON CONFLICT DO NOTHING. The rows'
-- observed_at values are relative to the FIRST seeding: four fall in the
-- last 7 days and two are 10 and 20 days old, which the Since-pill test
-- relies on. On a database seeded more than ~10 days earlier the window
-- has moved; a fresh database (CI) always matches.
--
-- The rows are shaped the way the push path writes them
-- (internal/evidence/ingest/ingest.go): server-observed fields in
-- `provenance`, the client's actor claim in `source_attribution`, the
-- pushed scope in `scope_canonical`, and `scope_id` NULL.
--
-- #2771 scope: records ...01, ...02 and ...05 were pushed in the seeded
-- cell (prod · us-east-1; ...05 also names us-west-2), ...03 and ...04 in
-- dev, and ...06 carries no scope, like a record pushed before scope
-- capture. The Scope pill narrows the six to three. #2794's record under
-- ...a003 (...07) was pushed in dev by test-connector-b, so it is in
-- neither the seeded cell nor connector-a's rows, and at 45 days old it
-- is outside every Since preset but "All time".

\set ON_ERROR_STOP on

BEGIN;

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-2755c0000001', 'Evidence list (e2e)', false)
ON CONFLICT DO NOTHING;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-2755c0000001';

-- The global SCF framework + current version, with the same ids and
-- rows as fixtures/e2e/controls-list.sql (whichever fixture runs first
-- inserts them; ON CONFLICT (id) makes the other a no-op). The CI e2e
-- database imports no SCF catalog, so the anchor needs them.
INSERT INTO frameworks (id, tenant_id, name, slug, issuer, description)
VALUES (
    'e2e74300-0000-0000-0000-0000000f0001',
    NULL,
    'Secure Controls Framework',
    'scf',
    'Secure Controls Framework Council',
    'Slice 743 e2e synthetic SCF spine — current version for the controls-list spec.'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO framework_versions (
    id, tenant_id, framework_id, version, effective_from, status, oscal_catalog_uri
)
VALUES (
    'e2e74300-0000-0000-0000-0000000f0002',
    NULL,
    'e2e74300-0000-0000-0000-0000000f0001',
    '2026.1-e2e',
    '2026-01-01',
    'current',
    'urn:scf:e2e:2026.1'
)
ON CONFLICT (id) DO NOTHING;

-- #2771: the Control pill's option is this anchor, and its value the
-- tenant control behind it (...a001).
INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
VALUES (
    '00000000-0000-0000-0000-2755c000a0a1',
    'e2e74300-0000-0000-0000-0000000f0002',
    '0E2E2771-1',
    'Configuration',
    'Configuration baseline (evidence-list e2e fixture anchor)',
    'Synthetic SCF anchor seeded for the evidence-list e2e spec.'
), (
    '00000000-0000-0000-0000-2755c000a0a3',
    'e2e74300-0000-0000-0000-0000000f0002',
    '0E2E2771-2',
    'Configuration',
    'Second anchored control (evidence-list e2e fixture anchor)',
    'Synthetic SCF anchor seeded for the evidence-list e2e spec (#2794).'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO controls (
    id, tenant_id, scf_id, scf_anchor_id, title, control_family,
    implementation_type, lifecycle_state, bundle_id
)
VALUES
    ('00000000-0000-0000-0000-2755c000a001',
     '00000000-0000-0000-0000-2755c0000001',
     '0E2E2771-1', '00000000-0000-0000-0000-2755c000a0a1',
     'Evidence list fixture — control with records', 'Configuration',
     'automated', 'active', 'e2e-evidence-list-with-rows'),
    ('00000000-0000-0000-0000-2755c000a002',
     '00000000-0000-0000-0000-2755c0000001',
     NULL, NULL,
     'Evidence list fixture — control without records', 'Configuration',
     'automated', 'active', 'e2e-evidence-list-no-rows'),
    ('00000000-0000-0000-0000-2755c000a003',
     '00000000-0000-0000-0000-2755c0000001',
     '0E2E2771-2', '00000000-0000-0000-0000-2755c000a0a3',
     'Evidence list fixture — second anchored control', 'Configuration',
     'automated', 'active', 'e2e-evidence-list-second-anchored')
ON CONFLICT DO NOTHING;

INSERT INTO scope_cells (id, tenant_id, label, dimensions, dimensions_hash)
VALUES (
    '00000000-0000-0000-0000-2755c000c311',
    '00000000-0000-0000-0000-2755c0000001',
    'prod · us-east-1',
    '{"environment":"prod","geography":"us-east-1"}'::jsonb,
    'e2e-evidence-list-cell-1'
)
ON CONFLICT DO NOTHING;

INSERT INTO evidence_records (
    id, tenant_id, control_id, control_ref, observed_at, provenance, result,
    payload, hash, ingestion_path, source_attribution, evidence_kind,
    schema_version, scope_canonical
)
SELECT
    v.id::uuid,
    '00000000-0000-0000-0000-2755c0000001',
    v.control::uuid,
    v.control,
    now() - v.age,
    jsonb_build_object(
        'ingestion_path', 'push',
        'credential_id', 'test-credential-e2e',
        'credential_type', 'api_key',
        'endpoint_version', 'v1',
        'schema_version', '1.0.0'
    ),
    v.result::evidence_result,
    jsonb_build_object('check', v.kind, 'fixture', 'evidence-list'),
    encode(sha256(convert_to(v.id, 'UTF8')), 'hex'),
    'push',
    jsonb_build_object('actor_type', 'connector', 'actor_id', v.actor),
    v.kind,
    '1.0.0',
    v.scope::jsonb
FROM (VALUES
    ('00000000-0000-0000-0000-2755c1000001', '00000000-0000-0000-0000-2755c000a001', interval '1 hour',  'pass',         'e2e.config_check.v1',  'test-connector-a', '[{"key":"environment","values":["prod"]},{"key":"geography","values":["us-east-1"]}]'),
    ('00000000-0000-0000-0000-2755c1000002', '00000000-0000-0000-0000-2755c000a001', interval '2 hours', 'fail',         'e2e.config_check.v1',  'test-connector-a', '[{"key":"environment","values":["prod"]},{"key":"geography","values":["us-east-1"]}]'),
    ('00000000-0000-0000-0000-2755c1000003', '00000000-0000-0000-0000-2755c000a001', interval '1 day',   'na',           'e2e.access_review.v1', 'test-connector-b', '[{"key":"environment","values":["dev"]},{"key":"geography","values":["us-east-1"]}]'),
    ('00000000-0000-0000-0000-2755c1000004', '00000000-0000-0000-0000-2755c000a001', interval '2 days',  'inconclusive', 'e2e.access_review.v1', 'test-connector-b', '[{"key":"environment","values":["dev"]},{"key":"geography","values":["us-east-1"]}]'),
    ('00000000-0000-0000-0000-2755c1000005', '00000000-0000-0000-0000-2755c000a001', interval '10 days', 'pass',         'e2e.config_check.v1',  'test-connector-a', '[{"key":"environment","values":["prod"]},{"key":"geography","values":["us-east-1","us-west-2"]}]'),
    ('00000000-0000-0000-0000-2755c1000006', '00000000-0000-0000-0000-2755c000a001', interval '20 days', 'pass',         'e2e.config_check.v1',  'test-connector-a', NULL),
    ('00000000-0000-0000-0000-2755c1000007', '00000000-0000-0000-0000-2755c000a003', interval '45 days', 'pass', 'e2e.config_check.v1', 'test-connector-b', '[{"key":"environment","values":["dev"]},{"key":"geography","values":["us-east-1"]}]')
) AS v (id, control, age, result, kind, actor, scope)
ON CONFLICT DO NOTHING;

COMMIT;
