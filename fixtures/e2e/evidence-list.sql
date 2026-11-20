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
-- `provenance`, the client's actor claim in `source_attribution`, and
-- `scope_id` NULL.

\set ON_ERROR_STOP on

BEGIN;

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-2755c0000001', 'Evidence list (e2e)', false)
ON CONFLICT DO NOTHING;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-2755c0000001';

INSERT INTO controls (
    id, tenant_id, title, control_family, implementation_type,
    lifecycle_state, bundle_id
)
VALUES
    ('00000000-0000-0000-0000-2755c000a001',
     '00000000-0000-0000-0000-2755c0000001',
     'Evidence list fixture — control with records', 'Configuration',
     'automated', 'active', 'e2e-evidence-list-with-rows'),
    ('00000000-0000-0000-0000-2755c000a002',
     '00000000-0000-0000-0000-2755c0000001',
     'Evidence list fixture — control without records', 'Configuration',
     'automated', 'active', 'e2e-evidence-list-no-rows')
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
    schema_version
)
SELECT
    v.id::uuid,
    '00000000-0000-0000-0000-2755c0000001',
    '00000000-0000-0000-0000-2755c000a001',
    '00000000-0000-0000-0000-2755c000a001',
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
    '1.0.0'
FROM (VALUES
    ('00000000-0000-0000-0000-2755c1000001', interval '1 hour',  'pass',         'e2e.config_check.v1',  'test-connector-a'),
    ('00000000-0000-0000-0000-2755c1000002', interval '2 hours', 'fail',         'e2e.config_check.v1',  'test-connector-a'),
    ('00000000-0000-0000-0000-2755c1000003', interval '1 day',   'na',           'e2e.access_review.v1', 'test-connector-b'),
    ('00000000-0000-0000-0000-2755c1000004', interval '2 days',  'inconclusive', 'e2e.access_review.v1', 'test-connector-b'),
    ('00000000-0000-0000-0000-2755c1000005', interval '10 days', 'pass',         'e2e.config_check.v1',  'test-connector-a'),
    ('00000000-0000-0000-0000-2755c1000006', interval '20 days', 'pass',         'e2e.config_check.v1',  'test-connector-a')
) AS v (id, age, result, kind, actor)
ON CONFLICT DO NOTHING;

COMMIT;
