-- #2755 — Playwright e2e seed for `web/e2e/metrics-dashboard.spec.ts`.
--
-- The metrics catalog and its cascade edges are global (tenant_id NULL,
-- loaded by migration), so this fixture seeds only the tenant-scoped
-- series the spec reads, in a tenant no other spec signs in to:
--
--   * audit_readiness_score (board, computed): three observations over
--     the last 90 days and a target, so its card shows a value, a
--     sparkline, a coloured badge and a "Show cascade" toggle.
--   * framework_coverage_pct (program, manual_input; parent
--     audit_readiness_score, child walkthrough_completion_rate): two
--     manual inputs, replicated into metric_observations with a
--     `manual:<user>` source by trg_metric_inputs_replicate, and a
--     target, so the detail page draws the chart, its overlays and the
--     audit-trail rows.
--
-- Rows carry fixed ids and ON CONFLICT DO NOTHING, so re-runs and
-- parallel workers apply the file safely. The spec's manual-input test
-- appends a row on every run; nothing here deletes, because each worker
-- applies this file in its own beforeAll and a delete would race a
-- running test. The spec asserts on its own submitted value, not on a
-- row count. The admin user is the JWT subject the spec mints; the
-- platform needs a user credential to accept a manual input.
--
-- Tenant and user UUIDs match TENANT / ADMIN_USER in the spec; the
-- `2755d` last-group prefix carries the ticket.

\set ON_ERROR_STOP on

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-2755d0000001', 'Metrics dashboard (e2e)', false)
ON CONFLICT DO NOTHING;

BEGIN;
SET LOCAL app.current_tenant = '00000000-0000-0000-0000-2755d0000001';

INSERT INTO users (id, tenant_id, email, display_name, status, idp_issuer, idp_subject)
VALUES ('44444444-4444-4444-4444-2755d0000011', '00000000-0000-0000-0000-2755d0000001',
        'metrics-dashboard-admin-e2e@example.invalid', 'Metrics Dashboard Admin E2E', 'active', '', '')
ON CONFLICT (id) DO UPDATE SET status = 'active';
INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES ('00000000-0000-0000-0000-2755d0000001', '44444444-4444-4444-4444-2755d0000011', 'admin', 'e2e:metrics-dashboard')
ON CONFLICT DO NOTHING;

-- Timestamps are relative to now() and refreshed on every apply (DO
-- UPDATE), so the 90-day window the page reads always holds them.
-- The two framework_coverage_pct rows carry the `manual:<user>` source
-- trg_metric_inputs_replicate writes for a manual input; they are
-- inserted as observations directly so their timestamps can be
-- refreshed (a re-applied metric_inputs row would fire no trigger).
INSERT INTO metric_observations (id, tenant_id, metric_id, observed_at, numeric_value, source)
VALUES
    ('55555555-5555-5555-5555-2755d0000001', '00000000-0000-0000-0000-2755d0000001', 'audit_readiness_score',
     now() - interval '60 days', 0.700000, 'e2e:metrics-dashboard'),
    ('55555555-5555-5555-5555-2755d0000002', '00000000-0000-0000-0000-2755d0000001', 'audit_readiness_score',
     now() - interval '30 days', 0.750000, 'e2e:metrics-dashboard'),
    ('55555555-5555-5555-5555-2755d0000003', '00000000-0000-0000-0000-2755d0000001', 'audit_readiness_score',
     now() - interval '1 day', 0.800000, 'e2e:metrics-dashboard'),
    ('55555555-5555-5555-5555-2755d0000004', '00000000-0000-0000-0000-2755d0000001', 'framework_coverage_pct',
     now() - interval '20 days', 0.600000, 'manual:44444444-4444-4444-4444-2755d0000011'),
    ('55555555-5555-5555-5555-2755d0000005', '00000000-0000-0000-0000-2755d0000001', 'framework_coverage_pct',
     now() - interval '2 days', 0.650000, 'manual:44444444-4444-4444-4444-2755d0000011')
ON CONFLICT (id) DO UPDATE SET observed_at = EXCLUDED.observed_at;

INSERT INTO metric_targets (tenant_id, metric_id, target_value, warning_threshold, critical_threshold, direction)
VALUES
    ('00000000-0000-0000-0000-2755d0000001', 'audit_readiness_score', 0.900000, 0.750000, 0.500000, 'higher_is_better'),
    ('00000000-0000-0000-0000-2755d0000001', 'framework_coverage_pct', 0.900000, 0.700000, 0.500000, 'higher_is_better')
ON CONFLICT (tenant_id, metric_id) DO NOTHING;
COMMIT;
