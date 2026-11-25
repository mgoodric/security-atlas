-- #2755 — Playwright e2e seed for `web/e2e/audits-list.spec.ts`.
--
-- Two tenants of the spec's own, so the list's counts, tally and empty
-- states never read periods other specs add to the demo tenant:
--
--   * 00000000-0000-0000-0000-2755a0000021 — three periods over its own
--     framework version: one frozen (lock icon, OSCAL download, tally),
--     one open period ending in ten days (the amber "ends in Nd" cue)
--     and one open period ending in 200 days (no cue). The two open
--     periods' dates are recomputed on every run so the cue stays inside
--     the 30-day window whatever day the suite runs.
--   * 00000000-0000-0000-0000-2755a0000022 — no periods at all, for the
--     true-zero empty state and the hidden tally.
--
-- The spec signs in to each tenant with an admin JWT minted by
-- /v1/test/issue-jwt (web/e2e/tenant-session.ts).

\set ON_ERROR_STOP on

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES
    ('00000000-0000-0000-0000-2755a0000021', 'Audits list (e2e)', false),
    ('00000000-0000-0000-0000-2755a0000022', 'Audits list empty (e2e)', false)
ON CONFLICT DO NOTHING;

BEGIN;
SET LOCAL app.current_tenant = '00000000-0000-0000-0000-2755a0000021';

INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
VALUES (
    '11111111-1111-1111-1111-2755a0000201',
    '00000000-0000-0000-0000-2755a0000021',
    'e2e-audits-list-fwk',
    'Audits List Framework',
    'Acme Industries',
    'Synthetic framework for the #2755 audits-list e2e spec.'
)
ON CONFLICT DO NOTHING;

INSERT INTO framework_versions (id, tenant_id, framework_id, version, effective_from)
VALUES (
    '11111111-1111-1111-1111-2755a0000202',
    '00000000-0000-0000-0000-2755a0000021',
    '11111111-1111-1111-1111-2755a0000201',
    '1.0',
    '2026-01-01'
)
ON CONFLICT DO NOTHING;

INSERT INTO audit_periods (
    id, tenant_id, name, framework_version_id, period_start, period_end,
    status, frozen_at, frozen_hash, frozen_by, created_by
)
VALUES (
    'cccccccc-cccc-cccc-cccc-2755a0000211',
    '00000000-0000-0000-0000-2755a0000021',
    'E2E audits-list frozen period',
    '11111111-1111-1111-1111-2755a0000202',
    '2026-01-01', '2026-03-31',
    'frozen', '2026-04-02 12:00:00+00', '\x2755'::bytea, 'e2e:audits-list',
    'e2e:audits-list'
)
ON CONFLICT DO NOTHING;

-- #3066: a frozen period carries its recorded control set; without one,
-- replay and import amendment fail closed (period.ErrNoControlSnapshot).
INSERT INTO audit_period_frozen_controls (tenant_id, audit_period_id, control_ids, source)
SELECT ap.tenant_id, ap.id,
       ARRAY(SELECT c.id FROM controls c WHERE c.tenant_id = ap.tenant_id ORDER BY c.id),
       'seed'
FROM audit_periods ap
WHERE ap.tenant_id = '00000000-0000-0000-0000-2755a0000021'
  AND ap.id = 'cccccccc-cccc-cccc-cccc-2755a0000211'
  AND ap.status = 'frozen'
ON CONFLICT DO NOTHING;

INSERT INTO audit_periods (
    id, tenant_id, name, framework_version_id, period_start, period_end,
    status, created_by
)
VALUES
(
    'cccccccc-cccc-cccc-cccc-2755a0000212',
    '00000000-0000-0000-0000-2755a0000021',
    'E2E audits-list period ending soon',
    '11111111-1111-1111-1111-2755a0000202',
    (now() - interval '60 days')::date, (now() + interval '10 days')::date,
    'open', 'e2e:audits-list'
),
(
    'cccccccc-cccc-cccc-cccc-2755a0000213',
    '00000000-0000-0000-0000-2755a0000021',
    'E2E audits-list period ending later',
    '11111111-1111-1111-1111-2755a0000202',
    (now() - interval '10 days')::date, (now() + interval '200 days')::date,
    'open', 'e2e:audits-list'
)
ON CONFLICT (id) DO UPDATE SET
    period_start = EXCLUDED.period_start,
    period_end = EXCLUDED.period_end;
COMMIT;
