-- #2081 — Playwright e2e seed for `web/e2e/sidebar-nav.spec.ts`.
--
-- The sidebar's flag gate is resolved server-side from the tenant's
-- feature_flags rows, which a browser route-mock cannot reach. The
-- flag-ON case therefore needs a tenant whose `board.reporting` and
-- `oscal.export` flags are on. Turning them on for the shared demo
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
    ('00000000-0000-0000-0000-000000002081', 'oscal.export', true,
     'OSCAL export. On for the #2081 sidebar spec tenant.', 'integrations', 'e2e-fixture', now())
ON CONFLICT (tenant_id, flag_key) DO UPDATE SET enabled = true;
