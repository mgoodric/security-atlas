-- Slice 070 — fixture for `audit-period-freezing.md`.
--
-- Seeds one open audit period for the demo tenant. The walkthrough
-- creates evidence records straddling the freeze boundary to
-- demonstrate the sample-population horizon.
--
-- The framework_version is the one installed by 00-seed.sql; the period
-- runs Q1 2026.

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

INSERT INTO audit_periods (
    id, tenant_id, name, framework_version_id, period_start, period_end,
    status, created_by
)
VALUES (
    '55555555-5555-5555-5555-555555550001',
    '00000000-0000-0000-0000-00000000d3a0',
    'SOC2 Q1 2026',
    '11111111-1111-1111-1111-111111110002',
    '2026-01-01',
    '2026-03-31',
    'open',
    'demo-operator@example.invalid'
)
ON CONFLICT DO NOTHING;

-- Three evidence records: two BEFORE the freeze cutoff, one AFTER.
-- The walkthrough freezes at 2026-04-15T12:00Z; observations:
--   - 2026-02-01 (in-period, in-freeze)
--   - 2026-03-15 (in-period, in-freeze)
--   - 2026-05-01 (post-freeze; must NOT appear in samples)

INSERT INTO evidence_records (
    id, tenant_id, control_id, scope_id, observed_at, provenance, result,
    payload, hash, freshness_class, ingestion_path, source_attribution,
    control_ref, evidence_kind, schema_version
)
VALUES
(
    '66666666-6666-6666-6666-666666660001',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330001',
    '22222222-2222-2222-2222-222222220001',
    '2026-02-01T00:00:00Z',
    '{"source":"walkthrough-fixture"}'::jsonb,
    'pass',
    '{"bucket":"acme-prod-customer-1","encrypted":true}'::jsonb,
    'sha256-fixture-01',
    'monthly',
    'push',
    '{"actor":"demo-operator@example.invalid"}'::jsonb,
    'CRY-05',
    'demo.encryption_state.v1',
    '1.0.0'
),
(
    '66666666-6666-6666-6666-666666660002',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330001',
    '22222222-2222-2222-2222-222222220001',
    '2026-03-15T00:00:00Z',
    '{"source":"walkthrough-fixture"}'::jsonb,
    'pass',
    '{"bucket":"acme-prod-customer-2","encrypted":true}'::jsonb,
    'sha256-fixture-02',
    'monthly',
    'push',
    '{"actor":"demo-operator@example.invalid"}'::jsonb,
    'CRY-05',
    'demo.encryption_state.v1',
    '1.0.0'
),
(
    '66666666-6666-6666-6666-666666660003',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-333333330001',
    '22222222-2222-2222-2222-222222220001',
    '2026-05-01T00:00:00Z',
    '{"source":"walkthrough-fixture"}'::jsonb,
    'pass',
    '{"bucket":"acme-prod-customer-3","encrypted":true}'::jsonb,
    'sha256-fixture-03',
    'monthly',
    'push',
    '{"actor":"demo-operator@example.invalid"}'::jsonb,
    'CRY-05',
    'demo.encryption_state.v1',
    '1.0.0'
)
ON CONFLICT DO NOTHING;

COMMIT;
