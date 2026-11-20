-- #2755 — Playwright e2e seed for `web/e2e/evidence-pagination.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The spec pages through the tenant-wide ledger, so it needs a
-- known row count and signs in to a tenant of its own: 120 evidence
-- records on one control, one minute apart. With the upstream page size
-- of 50 (internal/api/controldetail/pagination.go) that is three pages
-- (50, 50, 20): page 1 and page 2 have a Next, page 3 does not. Results
-- alternate pass / fail so the Result pill has rows either way.
--
-- Evidence is an append-only ledger (canvas §4.3), so this file only
-- ever inserts, keyed on fixed ids with ON CONFLICT DO NOTHING. The rows'
-- observed_at values are relative to the FIRST seeding and fall inside
-- the ledger's default 30-day window for 30 days after it.

\set ON_ERROR_STOP on

BEGIN;

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-2755c0000002', 'Evidence pagination (e2e)', false)
ON CONFLICT DO NOTHING;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-2755c0000002';

INSERT INTO controls (
    id, tenant_id, title, control_family, implementation_type,
    lifecycle_state, bundle_id
)
VALUES (
    '00000000-0000-0000-0000-2755c000b001',
    '00000000-0000-0000-0000-2755c0000002',
    'Evidence pagination fixture control', 'Configuration',
    'automated', 'active', 'e2e-evidence-pagination'
)
ON CONFLICT DO NOTHING;

INSERT INTO evidence_records (
    id, tenant_id, control_id, control_ref, observed_at, provenance, result,
    payload, hash, ingestion_path, source_attribution, evidence_kind,
    schema_version
)
SELECT
    ('00000000-0000-0000-0000-2755c2' || lpad(n::text, 6, '0'))::uuid,
    '00000000-0000-0000-0000-2755c0000002',
    '00000000-0000-0000-0000-2755c000b001',
    '00000000-0000-0000-0000-2755c000b001',
    now() - make_interval(mins => n),
    jsonb_build_object(
        'ingestion_path', 'push',
        'credential_id', 'test-credential-e2e',
        'credential_type', 'api_key',
        'endpoint_version', 'v1',
        'schema_version', '1.0.0'
    ),
    (CASE WHEN n % 2 = 0 THEN 'pass' ELSE 'fail' END)::evidence_result,
    jsonb_build_object('fixture', 'evidence-pagination', 'n', n),
    encode(sha256(convert_to('evidence-pagination-' || n, 'UTF8')), 'hex'),
    'push',
    jsonb_build_object('actor_type', 'connector', 'actor_id', 'test-connector-p'),
    'e2e.config_check.v1',
    '1.0.0'
FROM generate_series(1, 120) AS n
ON CONFLICT DO NOTHING;

COMMIT;
