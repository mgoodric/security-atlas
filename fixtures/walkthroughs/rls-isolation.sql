-- Slice 070 — fixture for `rls-tenant-isolation.md`.
--
-- Adds a SECOND tenant + a control owned by that tenant so the
-- walkthrough can demonstrate cross-tenant isolation. The "alt-tenant"
-- UUID is intentionally distinct from "demo-tenant" so a reviewer can
-- spot tenant-id leakage at a glance.
--
-- alt-tenant : 00000000-0000-0000-0000-00000000a17e ("alt-e")
-- demo-tenant: 00000000-0000-0000-0000-00000000d3a0 ("demo")

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000a17e';

-- Alt-tenant scope cell (different cloud account, distinct from
-- demo-tenant's '...123456789012').
INSERT INTO scopes (
    id, tenant_id, business_unit, environment, geography, cloud_account,
    data_classification, product_line
)
VALUES (
    '22222222-2222-2222-2222-2222222200a1',
    '00000000-0000-0000-0000-00000000a17e',
    'Alt Tenant · Platform',
    'prod',
    'us-west-2',
    'demo-aws-999999999999',
    'restricted',
    'alt-platform'
)
ON CONFLICT DO NOTHING;

-- Alt-tenant control. Same SCF anchor, different bundle_id (per-tenant
-- uniqueness is enforced by `controls_one_active_version_per_bundle`).
INSERT INTO controls (
    id, tenant_id, scf_id, title, description, control_family,
    implementation_type, owner_role, lifecycle_state, applicability_expr,
    bundle_id, freshness_class
)
VALUES (
    '333333aa-3333-3333-3333-3333333300a1',
    '00000000-0000-0000-0000-00000000a17e',
    'CRY-05',
    'Encryption at rest — alt-tenant production buckets',
    'Alt-tenant control. Visible only when app.current_tenant is the alt-tenant UUID.',
    'Cryptography',
    'automated',
    'platform-eng',
    'active',
    'env == "prod"',
    'alt-tenant-s3-encryption',
    'monthly'
)
ON CONFLICT DO NOTHING;

COMMIT;
