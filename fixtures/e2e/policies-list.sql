-- #2755 — Playwright e2e seed for `web/e2e/policies-list.spec.ts`.
--
-- Two tenants no other spec signs in to:
--
--   * POLICIES (…2755d0000002): three policies across two statuses and
--     two owner roles, so the filter pills have something to narrow and
--     the ack-rate column shows both a bar and an em-dash:
--       - "Access control policy"   published, owner security,
--         audience viewer, 2 of 2 viewers acknowledged  (100%, >= 95%)
--       - "Change management policy" published, owner engineering,
--         audience viewer, 1 of 2 viewers acknowledged  (50%)
--       - "Data retention policy"    draft, owner security (no ack cell)
--   * EMPTY (…2755d0000003): no policies at all, for the zero-state.
--
-- The two viewers are active users holding a live `viewer` user_roles
-- row, which is the ack-rate denominator (#2586). A published policy's
-- acknowledgments reference the policy row itself as the version.
--
-- Idempotent: fixed ids and ON CONFLICT DO NOTHING. Tenant UUIDs match
-- POLICIES_TENANT / EMPTY_TENANT in the spec; the `2755d` last-group
-- prefix carries the ticket.

\set ON_ERROR_STOP on

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES
    ('00000000-0000-0000-0000-2755d0000002', 'Policies list (e2e)', false),
    ('00000000-0000-0000-0000-2755d0000003', 'Policies list empty (e2e)', false)
ON CONFLICT DO NOTHING;

BEGIN;
SET LOCAL app.current_tenant = '00000000-0000-0000-0000-2755d0000002';

INSERT INTO users (id, tenant_id, email, display_name, status, idp_issuer, idp_subject)
VALUES
    ('44444444-4444-4444-4444-2755d0000021', '00000000-0000-0000-0000-2755d0000002',
     'policies-list-viewer-a-e2e@example.invalid', 'Policies List Viewer A', 'active', '', ''),
    ('44444444-4444-4444-4444-2755d0000022', '00000000-0000-0000-0000-2755d0000002',
     'policies-list-viewer-b-e2e@example.invalid', 'Policies List Viewer B', 'active', '', '')
ON CONFLICT (id) DO UPDATE SET status = 'active';

INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES
    ('00000000-0000-0000-0000-2755d0000002', '44444444-4444-4444-4444-2755d0000021', 'viewer', 'e2e:policies-list'),
    ('00000000-0000-0000-0000-2755d0000002', '44444444-4444-4444-4444-2755d0000022', 'viewer', 'e2e:policies-list')
ON CONFLICT DO NOTHING;

INSERT INTO policies (
    id, tenant_id, title, version, effective_date, body_md,
    acknowledgment_required_roles, status, owner_role, approver_role,
    created_by, published_at, published_by
)
VALUES
    ('77777777-7777-7777-7777-2755d0000001', '00000000-0000-0000-0000-2755d0000002',
     'Access control policy', '1.0.0', CURRENT_DATE - 30, 'Access to systems is granted by role.',
     ARRAY['viewer'], 'published', 'security', 'admin',
     'e2e:policies-list', now() - interval '30 days', 'e2e:policies-list'),
    ('77777777-7777-7777-7777-2755d0000002', '00000000-0000-0000-0000-2755d0000002',
     'Change management policy', '1.0.0', CURRENT_DATE - 20, 'Changes are reviewed before release.',
     ARRAY['viewer'], 'published', 'engineering', 'admin',
     'e2e:policies-list', now() - interval '20 days', 'e2e:policies-list'),
    ('77777777-7777-7777-7777-2755d0000003', '00000000-0000-0000-0000-2755d0000002',
     'Data retention policy', '0.1.0', NULL, 'Records are kept for a defined period.',
     ARRAY['viewer'], 'draft', 'security', 'admin',
     'e2e:policies-list', NULL, NULL)
ON CONFLICT (id) DO NOTHING;

INSERT INTO policy_acknowledgments (id, tenant_id, policy_id, policy_version_id, user_id, acknowledged_at)
VALUES
    ('88888888-8888-8888-8888-2755d0000001', '00000000-0000-0000-0000-2755d0000002',
     '77777777-7777-7777-7777-2755d0000001', '77777777-7777-7777-7777-2755d0000001',
     '44444444-4444-4444-4444-2755d0000021', now() - interval '10 days'),
    ('88888888-8888-8888-8888-2755d0000002', '00000000-0000-0000-0000-2755d0000002',
     '77777777-7777-7777-7777-2755d0000001', '77777777-7777-7777-7777-2755d0000001',
     '44444444-4444-4444-4444-2755d0000022', now() - interval '10 days'),
    ('88888888-8888-8888-8888-2755d0000003', '00000000-0000-0000-0000-2755d0000002',
     '77777777-7777-7777-7777-2755d0000002', '77777777-7777-7777-7777-2755d0000002',
     '44444444-4444-4444-4444-2755d0000021', now() - interval '5 days')
ON CONFLICT (id) DO NOTHING;
COMMIT;
