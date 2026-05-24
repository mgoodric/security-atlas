-- Slice 223 — Playwright e2e seed for `web/e2e/controls-top-bar.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The controls-top-bar spec asserts the slice-223 shared-
-- shell chrome (breadcrumb + global search) renders correctly:
--
--   - Breadcrumb: reads /api/me/tenants. The slice-192 handler joins
--     the JWT's `available_tenants[]` claim against the `tenants`
--     table (slice 144) for the human-readable name. The default
--     bootstrap seed inserts "Default Tenant" for one canonical UUID
--     but the e2e harness mints a JWT for DEMO_TENANT_ID (d3a0...)
--     which does NOT have a tenants row by default. We insert a row
--     with name="Demo Tenant" so the breadcrumb has a non-empty left
--     segment to render (the component renders null when the name
--     resolves to empty/whitespace per the pickCurrentTenantName
--     contract).
--
--   - Global search input: the input always renders (independent of
--     data). ⌘K focus + typing-into-input assertions don't need new
--     rows; the spec mocks `/api/search` via page.route to inject a
--     deterministic hits payload, mirroring slice 214's pattern.
--
-- All inserts are ON CONFLICT DO NOTHING for idempotency. The
-- "Demo Tenant" name is benign — no PII, no maintainer-identifying
-- string, matches the demo-* naming used elsewhere.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

-- Slice 144 + slice 192: the tenants row that /v1/me/tenants joins
-- the JWT's available_tenants[] claim against. Without this row the
-- breadcrumb's left segment resolves to empty and the chrome stays
-- null (the load-bearing pickCurrentTenantName contract).
--
-- The bootstrap seed inserts "Default Tenant" for a different
-- canonical UUID (000-0000-4000-8000-0001). The e2e harness mints a
-- JWT for d3a0 (web/e2e/seed.ts DEMO_TENANT_ID); seeding the row
-- under THAT UUID is what closes the breadcrumb's data dependency.
INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES (
    '00000000-0000-0000-0000-00000000d3a0',
    'Demo Tenant',
    FALSE
)
ON CONFLICT (id) DO NOTHING;

COMMIT;
