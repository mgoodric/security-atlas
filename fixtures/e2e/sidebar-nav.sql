-- #2081 — Playwright e2e seed for `web/e2e/sidebar-nav.spec.ts`.
--
-- The sidebar's flag gate is resolved server-side from the tenant's
-- feature_flags rows, which a browser route-mock cannot reach. The
-- flag-ON case therefore needs a tenant whose `board.reporting` and
-- `vendor.claims` flags are on. Turning them on for the shared demo
-- tenant (d3a0) would break the flag-OFF assertions other specs make in
-- parallel (feature-flag-gating.spec.ts, and this spec's own), so the
-- flags are turned on for a dedicated tenant no other spec signs in to.
--
-- Tenant UUID matches NAV_TENANT in web/e2e/sidebar-nav.spec.ts. The
-- `2081` suffix carries the ticket; no other fixture uses it.
--
-- Idempotent: ON CONFLICT keeps re-runs (and parallel workers) clean,
-- and the DO UPDATE pins the flags on even if a run left them off.

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-000000002081', 'Sidebar nav (flags on)', false)
ON CONFLICT DO NOTHING;

INSERT INTO feature_flags (tenant_id, flag_key, enabled, description, category, last_changed_by, last_changed_at)
VALUES
    ('00000000-0000-0000-0000-000000002081', 'board.reporting', true,
     'Board reporting module. On for the #2081 sidebar spec tenant.', 'board', 'e2e-fixture', now()),
    ('00000000-0000-0000-0000-000000002081', 'vendor.claims', true,
     'Vendor claims. On for the #2081 sidebar spec tenant.', 'vendor', 'e2e-fixture', now())
ON CONFLICT (tenant_id, flag_key) DO UPDATE SET enabled = true;

-- #2494: a viewer and an auditor user holding that role in user_roles,
-- for the "no Admin entry" cases. A role claim alone derives nothing
-- (#2381), and a user with no live role sees the no-role page, not the
-- sidebar. User UUIDs match ROLE_USER in web/e2e/sidebar-nav.spec.ts.
BEGIN;
SET LOCAL app.current_tenant = '00000000-0000-0000-0000-000000002081';
INSERT INTO users (id, tenant_id, email, display_name, status, idp_issuer, idp_subject)
VALUES
    ('44444444-4444-4444-4444-44444442081a', '00000000-0000-0000-0000-000000002081',
     'sidebar-nav-viewer-e2e@example.invalid', 'Sidebar Nav Viewer E2E', 'active', '', ''),
    ('44444444-4444-4444-4444-44444442081b', '00000000-0000-0000-0000-000000002081',
     'sidebar-nav-auditor-e2e@example.invalid', 'Sidebar Nav Auditor E2E', 'active', '', '')
ON CONFLICT (id) DO UPDATE SET status = 'active';
INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES
    ('00000000-0000-0000-0000-000000002081', '44444444-4444-4444-4444-44444442081a', 'viewer', 'e2e:sidebar-nav'),
    ('00000000-0000-0000-0000-000000002081', '44444444-4444-4444-4444-44444442081b', 'auditor', 'e2e:sidebar-nav')
ON CONFLICT DO NOTHING;
COMMIT;
