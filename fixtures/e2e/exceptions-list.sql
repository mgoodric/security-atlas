-- #2755 — Playwright e2e seed for `web/e2e/exceptions-list.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The spec asserts on row counts, an empty register and
-- cross-tenant isolation, so it uses three tenants of its own:
--
--   * tenant A (...2755c0000003) — one control and four exceptions, one
--     each in `requested`, `approved`, `active` and `expired`. None is
--     `denied`, which drives the filter-induced empty state.
--   * tenant E (...2755c0000004) — no exceptions (truly-zero state).
--   * tenant B (...2755c0000005) — one control and two exceptions that
--     tenant A must never see.
--
-- Exceptions are mutable lifecycle rows (not the evidence ledger), so
-- each run deletes the spec's exceptions in its own tenants and inserts
-- them again: a lifecycle sweep between runs cannot leave a row in a
-- status the spec does not expect, and requested_at stays recent enough
-- for the 365-day expiry check.

\set ON_ERROR_STOP on

BEGIN;

-- Playwright runs this file once per worker, possibly at the same time.
-- Serialise the delete-then-insert so a second run's DELETE (a fresh
-- statement snapshot once the lock is granted) sees the first run's rows
-- instead of colliding with them on the primary key.
SELECT pg_advisory_xact_lock(hashtext('e2e-exceptions-list'));

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES
    ('00000000-0000-0000-0000-2755c0000003', 'Exceptions list A (e2e)', false),
    ('00000000-0000-0000-0000-2755c0000004', 'Exceptions list empty (e2e)', false),
    ('00000000-0000-0000-0000-2755c0000005', 'Exceptions list B (e2e)', false)
ON CONFLICT DO NOTHING;

INSERT INTO controls (
    id, tenant_id, title, control_family, implementation_type,
    lifecycle_state, bundle_id
)
VALUES
    ('00000000-0000-0000-0000-2755c000c001',
     '00000000-0000-0000-0000-2755c0000003',
     'Exceptions list fixture control A', 'Configuration',
     'automated', 'active', 'e2e-exceptions-list-a'),
    ('00000000-0000-0000-0000-2755c000c002',
     '00000000-0000-0000-0000-2755c0000005',
     'Exceptions list fixture control B', 'Configuration',
     'automated', 'active', 'e2e-exceptions-list-b')
ON CONFLICT DO NOTHING;

DELETE FROM exceptions
WHERE tenant_id IN (
    '00000000-0000-0000-0000-2755c0000003',
    '00000000-0000-0000-0000-2755c0000004',
    '00000000-0000-0000-0000-2755c0000005'
);

INSERT INTO exceptions (
    id, tenant_id, control_id, scope_cell_predicate, justification,
    requested_by, requested_at, approved_by, approved_at, activated_by,
    activated_at, effective_from, expires_at, expired_at, status
)
VALUES
    ('00000000-0000-0000-0000-2755c00e0001',
     '00000000-0000-0000-0000-2755c0000003',
     '00000000-0000-0000-0000-2755c000c001', '{}'::jsonb,
     'Tenant A requested: legacy host awaiting decommission.',
     'test-requester', now() - interval '2 days', NULL, NULL, NULL, NULL,
     NULL, now() + interval '60 days', NULL, 'requested'),
    ('00000000-0000-0000-0000-2755c00e0002',
     '00000000-0000-0000-0000-2755c0000003',
     '00000000-0000-0000-0000-2755c000c001', '{}'::jsonb,
     'Tenant A approved: vendor patch scheduled next quarter.',
     'test-requester', now() - interval '5 days', 'test-approver',
     now() - interval '4 days', NULL, NULL, NULL,
     now() + interval '90 days', NULL, 'approved'),
    ('00000000-0000-0000-0000-2755c00e0003',
     '00000000-0000-0000-0000-2755c0000003',
     '00000000-0000-0000-0000-2755c000c001', '{}'::jsonb,
     'Tenant A active: compensating network control in place.',
     'test-requester', now() - interval '10 days', 'test-approver',
     now() - interval '9 days', 'test-approver', now() - interval '8 days',
     now() - interval '8 days', now() + interval '30 days', NULL, 'active'),
    ('00000000-0000-0000-0000-2755c00e0004',
     '00000000-0000-0000-0000-2755c0000003',
     '00000000-0000-0000-0000-2755c000c001', '{}'::jsonb,
     'Tenant A expired: migration window closed.',
     'test-requester', now() - interval '100 days', 'test-approver',
     now() - interval '99 days', 'test-approver', now() - interval '98 days',
     now() - interval '98 days', now() - interval '8 days',
     now() - interval '8 days', 'expired'),
    ('00000000-0000-0000-0000-2755c00e0011',
     '00000000-0000-0000-0000-2755c0000005',
     '00000000-0000-0000-0000-2755c000c002', '{}'::jsonb,
     'Tenant B only: this row must never reach tenant A.',
     'test-requester', now() - interval '3 days', NULL, NULL, NULL, NULL,
     NULL, now() + interval '45 days', NULL, 'requested'),
    ('00000000-0000-0000-0000-2755c00e0012',
     '00000000-0000-0000-0000-2755c0000005',
     '00000000-0000-0000-0000-2755c000c002', '{}'::jsonb,
     'Tenant B only: second row tenant A must not see.',
     'test-requester', now() - interval '6 days', 'test-approver',
     now() - interval '5 days', 'test-approver', now() - interval '4 days',
     now() - interval '4 days', now() + interval '20 days', NULL, 'active');

COMMIT;
