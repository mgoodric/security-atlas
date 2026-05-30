-- Slice 389 — Playwright e2e seed for `web/e2e/tenant-switch-rls.spec.ts`.
--
-- Establishes the preconditions the docker-compose bring-up cannot
-- otherwise provide for the REAL-Postgres-RLS cross-tenant-leak
-- assertion (the depth slice 351's mocked `tenant-switch.spec.ts`
-- deferred here). Applied by the harness AFTER
-- fixtures/walkthroughs/00-seed.sql (which seeds the demo tenant + a
-- control under d3a0) and AFTER the phase-2 migrations.
--
-- What this fixture creates:
--
--   1. Two canonical `tenants` identity rows (slice 144 table) so the
--      slice-192 `GET /v1/me/tenants` handler can enrich the switcher
--      dropdown with names. The handler issues
--      `SELECT id, name FROM tenants WHERE id = ANY($1)` against the
--      BYPASSRLS pool keyed on the JWT's available_tenants[] — without
--      these rows the dropdown labels would be empty.
--
--   2. A KNOWN risk row in TENANT A only. The spec authenticates as a
--      multi-tenant user (A + B), confirms the risk is visible while
--      current_tenant = A, switches to B via the real RFC 8693
--      token-exchange, and asserts the SAME risk is NOT visible while
--      current_tenant = B. The absence is enforced by the slice-005
--      RLS policy `current_tenant_matches(tenant_id)` on `risks` —
--      constitutional invariant #6, the v1 binary tenant-isolation
--      criterion.
--
-- Tenant UUIDs match the constants in web/e2e/tenant-switch-rls.spec.ts:
--
--   TENANT_A = aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa
--   TENANT_B = bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb
--
-- These are visually distinct ("aaaa" / "bbbb") so a reviewer scanning
-- a query plan can spot tenant-id leakage at a glance — same discipline
-- as the d3a0/a17e demo/alt UUIDs in 00-seed.sql.
--
-- The risk id (eeeeeeee-...-tenant-A-only) carries its provenance in the
-- UUID itself: "ee" prefix + "0001" suffix, distinct from the slice-082
-- dashboard fixture's 7777...0001 risk so the two fixtures coexist in
-- the same additive CI run (web/e2e/README.md seed-harness rule 3).
--
-- Idempotent: every INSERT uses ON CONFLICT DO NOTHING. The `tenants`
-- table is RLS-protected (FORCE), but the demo-tenant superuser psql
-- session (DATABASE_URL = postgres superuser in CI) bypasses RLS for
-- the seed write; production never runs this file.

\set ON_ERROR_STOP on

-- ============================================================
-- Tenant identity rows (slice 144 `tenants` table)
-- ============================================================
-- The seed runs as the Postgres superuser (DATABASE_URL), which is
-- BYPASSRLS, so no SET LOCAL app.current_tenant is required to insert
-- across both tenants here. We still scope the risk INSERT below under
-- the correct GUC for clarity + defense-in-depth.
INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES
    ('aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa', 'Acme Security (tenant A)', false),
    ('bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb', 'Globex Compliance (tenant B)', false)
ON CONFLICT DO NOTHING;

-- ============================================================
-- A KNOWN risk in TENANT A only
-- ============================================================
BEGIN;

SET LOCAL app.current_tenant = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa';

INSERT INTO risks (
    id, tenant_id, title, description, category, treatment,
    treatment_owner, inherent_score, residual_score
)
VALUES (
    'eeeeeeee-eeee-eeee-eeee-eeeeeeee0001',
    'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa',
    'Tenant-A-only cross-tenant-leak canary risk',
    'A risk seeded only in tenant A. The slice 389 e2e spec asserts this row is invisible from tenant B through real PostgreSQL RLS.',
    'confidentiality',
    'mitigate',
    'security-engineering',
    '{"likelihood": 3, "impact": 4}'::jsonb,
    '{"likelihood": 1, "impact": 4}'::jsonb
)
ON CONFLICT DO NOTHING;

COMMIT;
