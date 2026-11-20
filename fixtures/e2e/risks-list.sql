-- #2755 — Playwright e2e seed for `web/e2e/risks-list.spec.ts`.
--
-- A tenant no other spec signs in to, holding a register big enough to
-- page and varied enough to filter:
--
--   * 55 open risks ("Register risk 01" .. "Register risk 55"), so the
--     default 50-row page leaves 5 rows on page 2.
--   * Risks 01-30 are `mitigate`, 31-55 `untreated`; none is `avoid`,
--     so a treatment=avoid filter is empty by construction.
--   * Two categories (operational, compliance), alternating.
--   * Risk 01 alone is owned by the tenant's one user and sits in the
--     tenant's one org unit, so the six-key URL round-trip lands on a
--     real row.
--
-- Idempotent: fixed ids and ON CONFLICT DO NOTHING. Tenant, user and
-- org-unit UUIDs match the constants in the spec; the `2755d`
-- last-group prefix carries the ticket.

\set ON_ERROR_STOP on

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-2755d0000004', 'Risks list (e2e)', false)
ON CONFLICT DO NOTHING;

BEGIN;
SET LOCAL app.current_tenant = '00000000-0000-0000-0000-2755d0000004';

INSERT INTO users (id, tenant_id, email, display_name, status, idp_issuer, idp_subject)
VALUES ('44444444-4444-4444-4444-2755d0000041', '00000000-0000-0000-0000-2755d0000004',
        'risks-list-owner-e2e@example.invalid', 'Risks List Owner', 'active', '', '')
ON CONFLICT (id) DO UPDATE SET status = 'active';
INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES ('00000000-0000-0000-0000-2755d0000004', '44444444-4444-4444-4444-2755d0000041', 'admin', 'e2e:risks-list')
ON CONFLICT DO NOTHING;

INSERT INTO org_risk_categories (id, tenant_id, slug, name, is_default)
VALUES
    ('aaaaaaaa-aaaa-aaaa-aaaa-2755d0000001', '00000000-0000-0000-0000-2755d0000004',
     'operational', 'Operational', true),
    ('aaaaaaaa-aaaa-aaaa-aaaa-2755d0000002', '00000000-0000-0000-0000-2755d0000004',
     'compliance', 'Compliance', false)
ON CONFLICT DO NOTHING;

INSERT INTO org_units (id, tenant_id, name, parent_id, level)
VALUES ('cccccccc-cccc-cccc-cccc-2755d0000001', '00000000-0000-0000-0000-2755d0000004',
        'Platform', NULL, 'org')
ON CONFLICT DO NOTHING;

INSERT INTO risks (
    id, tenant_id, title, description, category_id, treatment,
    inherent_score, owner_user_id, org_unit_id
)
SELECT
    ('99999999-9999-9999-9999-2755d' || lpad(i::text, 7, '0'))::uuid,
    '00000000-0000-0000-0000-2755d0000004',
    'Register risk ' || lpad(i::text, 2, '0'),
    'Seeded register row for the risks list e2e spec.',
    CASE WHEN i % 2 = 1
         THEN 'aaaaaaaa-aaaa-aaaa-aaaa-2755d0000001'::uuid
         ELSE 'aaaaaaaa-aaaa-aaaa-aaaa-2755d0000002'::uuid END,
    (CASE WHEN i <= 30 THEN 'mitigate' ELSE 'untreated' END)::risk_treatment,
    jsonb_build_object('likelihood', 1 + i % 5, 'impact', 1 + (i / 5) % 5),
    CASE WHEN i = 1 THEN '44444444-4444-4444-4444-2755d0000041'::uuid END,
    CASE WHEN i = 1 THEN 'cccccccc-cccc-cccc-cccc-2755d0000001'::uuid END
FROM generate_series(1, 55) AS i
ON CONFLICT (id) DO NOTHING;
COMMIT;
