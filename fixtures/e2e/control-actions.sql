-- #2110 — a tenant of control-actions-2110.spec.ts's own.
--
-- The spec uploads, evaluates and supersedes a real control. In the shared
-- demo tenant that changes the active control set other specs count while
-- they run in parallel (controls-list.spec.ts select-all read 6 for 5), so
-- the spec signs in to this tenant instead. It needs no other rows: the
-- spec's bundle declares anchor_fit: none, so no SCF catalog is required.
\set ON_ERROR_STOP on

BEGIN;

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-000000002110', 'Control actions (#2110)', false)
ON CONFLICT DO NOTHING;

COMMIT;
