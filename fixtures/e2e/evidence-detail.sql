-- #2137 — Playwright e2e seed for `web/e2e/evidence-detail.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). Seeds one manual attestation in the demo tenant, on the base
-- seed's control, so the spec can open it from the evidence list and read
-- its statement and result on the detail page. The id and statement are
-- distinct from every other fixture (fixtures are additive within a run).

BEGIN;

INSERT INTO evidence_records (
    id, tenant_id, control_id, observed_at, provenance, result,
    payload, hash, freshness_class, ingestion_path, source_attribution,
    control_ref, evidence_kind, schema_version
)
VALUES (
    '66666666-6666-6666-6666-66666666d137',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330001',
    now() - interval '1 hour',
    '{"source":"e2e-fixture"}'::jsonb,
    'pass',
    '{"attestor":"demo-operator@example.invalid","statement":"Evidence-detail fixture: quarterly console access reviewed, no exceptions.","supporting_uri":"https://example.invalid/reviews/q3"}'::jsonb,
    'sha256-evidence-detail-fixture-01',
    'quarterly',
    'manual_upload',
    '{"actor_type":"user","actor_id":"demo-operator@example.invalid"}'::jsonb,
    '33333333-3333-3333-3333-333333330001',
    'manual.attestation.v1',
    '1.1.0'
)
ON CONFLICT DO NOTHING;

COMMIT;
