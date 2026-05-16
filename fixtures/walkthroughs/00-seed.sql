-- Slice 070 — base seed data shared by all five walkthroughs.
--
-- Applied as the FIRST step of `just walkthroughs-refresh`, before any
-- per-walkthrough SQL. All UUIDs are deterministic so re-runs produce
-- byte-identical output (modulo showboat header).
--
-- Neutrality constraints (see fixtures/walkthroughs/README.md):
--   - tenant: 'demo-tenant'
--   - customer: 'Acme Industries'
--   - user: 'demo-operator@example.invalid'
--   - no maintainer references, no vendor token prefixes, no PII
--
-- Idempotent: every INSERT uses ON CONFLICT DO NOTHING.

-- ============================================================
-- Tenant context
-- ============================================================
-- security-atlas does not have a `tenants` table — tenancy is a uuid
-- column on every primary-key table, set per-request via
-- `app.current_tenant`. The "demo-tenant" UUID below is the value the
-- walkthroughs SET LOCAL during their SQL blocks.
--
-- demo-tenant            : 00000000-0000-0000-0000-00000000d3a0
-- alt-tenant (RLS demo)  : 00000000-0000-0000-0000-00000000a17e
--
-- These are NOT random — they are visually distinct ("d3a0" / "a17e")
-- so a reviewer scanning a query plan can spot tenant-id leakage at a
-- glance.

-- ============================================================
-- Framework + framework_version + a single requirement
-- ============================================================
-- The walkthroughs reference a tiny synthetic framework with one
-- requirement. Real catalogs are imported by slice 006.

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
VALUES (
    '11111111-1111-1111-1111-111111110001',
    '00000000-0000-0000-0000-00000000d3a0',
    'demo-fwk',
    'Demo Framework',
    'Acme Industries',
    'Synthetic framework used only by the slice 070 onboarding walkthroughs.'
)
ON CONFLICT DO NOTHING;

INSERT INTO framework_versions (id, tenant_id, framework_id, version, effective_from, oscal_catalog_uri)
VALUES (
    '11111111-1111-1111-1111-111111110002',
    '00000000-0000-0000-0000-00000000d3a0',
    '11111111-1111-1111-1111-111111110001',
    '1.0',
    '2026-01-01',
    'urn:demo:framework:1.0'
)
ON CONFLICT DO NOTHING;

INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
VALUES (
    '11111111-1111-1111-1111-111111110003',
    '11111111-1111-1111-1111-111111110002',
    'DEMO-1',
    'Encryption at rest',
    'All customer data stored in production object stores must be encrypted at rest.'
)
ON CONFLICT DO NOTHING;

-- ============================================================
-- Scope cell
-- ============================================================
INSERT INTO scopes (
    id, tenant_id, business_unit, environment, geography, cloud_account,
    data_classification, product_line
)
VALUES (
    '22222222-2222-2222-2222-222222220001',
    '00000000-0000-0000-0000-00000000d3a0',
    'Acme Industries · Engineering',
    'prod',
    'us-east-1',
    'demo-aws-123456789012',
    'confidential',
    'acme-saas-core'
)
ON CONFLICT DO NOTHING;

-- ============================================================
-- One control covering DEMO-1
-- ============================================================
INSERT INTO controls (
    id, tenant_id, scf_id, title, description, control_family,
    implementation_type, owner_role, lifecycle_state, applicability_expr,
    bundle_id, freshness_class
)
VALUES (
    '33333333-3333-3333-3333-333333330001',
    '00000000-0000-0000-0000-00000000d3a0',
    'CRY-05',
    'Encryption at rest — production object stores',
    'Production S3 buckets in customer-data accounts have server-side encryption enabled.',
    'Cryptography',
    'automated',
    'security-engineering',
    'active',
    'env == "prod" AND data_classification == "confidential"',
    'demo-s3-encryption',
    'monthly'
)
ON CONFLICT DO NOTHING;

COMMIT;
