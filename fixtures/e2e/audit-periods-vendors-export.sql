-- #2755 — Playwright e2e seed for
-- `web/e2e/audit-periods-vendors-export.spec.ts`.
--
-- A tenant of the spec's own with one audit period (over its own
-- framework version) and one vendor, so the /audits and /vendors export
-- buttons have rows to export. The vendor's free-text owner_user is
-- email-shaped: the export masks it to `*@<domain>`, and the spec checks
-- the local-part `export-owner-e2e` never reaches the wire.
--
-- Tenant UUID matches TENANT in the spec.

\set ON_ERROR_STOP on

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-2755a0000011', 'Export buttons (e2e)', false)
ON CONFLICT DO NOTHING;

BEGIN;
SET LOCAL app.current_tenant = '00000000-0000-0000-0000-2755a0000011';

INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
VALUES (
    '11111111-1111-1111-1111-2755a0000101',
    '00000000-0000-0000-0000-2755a0000011',
    'e2e-export-fwk',
    'Export Framework',
    'Acme Industries',
    'Synthetic framework for the #2755 export-buttons e2e spec.'
)
ON CONFLICT DO NOTHING;

INSERT INTO framework_versions (id, tenant_id, framework_id, version, effective_from)
VALUES (
    '11111111-1111-1111-1111-2755a0000102',
    '00000000-0000-0000-0000-2755a0000011',
    '11111111-1111-1111-1111-2755a0000101',
    '1.0',
    '2026-01-01'
)
ON CONFLICT DO NOTHING;

INSERT INTO audit_periods (
    id, tenant_id, name, framework_version_id, period_start, period_end,
    status, created_by
)
VALUES (
    'cccccccc-cccc-cccc-cccc-2755a0000111',
    '00000000-0000-0000-0000-2755a0000011',
    'E2E export period',
    '11111111-1111-1111-1111-2755a0000102',
    '2026-01-01', '2026-12-31',
    'open', 'e2e:export'
)
ON CONFLICT DO NOTHING;

INSERT INTO vendors (id, tenant_id, name, criticality, owner_user)
VALUES (
    '77777777-7777-7777-7777-2755a0000111',
    '00000000-0000-0000-0000-2755a0000011',
    'E2E export vendor',
    'medium',
    'export-owner-e2e@operator.example.invalid'
)
ON CONFLICT (id) DO UPDATE SET owner_user = EXCLUDED.owner_user;
COMMIT;
