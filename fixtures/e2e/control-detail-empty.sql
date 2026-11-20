-- Slice 160 — Playwright e2e seed for `web/e2e/control-detail-empty.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The spec's scenario is a fresh install: a tenant with ZERO
-- instantiated controls. The demo tenant cannot stand in for that — the
-- base seed and other fixtures put controls in it — so this fixture
-- creates a tenant of the spec's own and inserts nothing else into it.
-- The spec mints an admin JWT for that tenant through /v1/test/issue-jwt.
--
-- Tenant UUID matches TENANT in the spec; the `2711` suffix carries the
-- ticket that restored the spec's assertions. The bogus control UUID the
-- spec navigates to, `00000000-0000-0000-0000-000000000152`, matches no
-- row by construction.

\set ON_ERROR_STOP on

INSERT INTO tenants (id, name, is_bootstrap_tenant)
VALUES ('00000000-0000-0000-0000-000000002711', 'Control detail empty (e2e)', false)
ON CONFLICT DO NOTHING;
