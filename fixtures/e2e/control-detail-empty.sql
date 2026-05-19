-- Slice 160 — Playwright e2e seed for `web/e2e/control-detail-empty.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The control-detail-empty spec's preconditions (per its
-- preamble):
--
--   - TEST_BEARER carries a credential in a tenant with ZERO
--     instantiated controls (the slice 152 fresh-install scenario).
--   - BOGUS_CONTROL_ID is a syntactically-valid UUID that the platform
--     guarantees does not resolve in this tenant. The fixture publishes
--     `00000000-0000-0000-0000-000000000152` as the canonical value.
--
-- The "empty state" is the absence of inserts — this fixture
-- intentionally does NOT add any tenant_controls rows. The harness
-- baseline (00-seed.sql) already establishes the tenant + auth; this
-- fixture just opens + closes a transaction in that tenant's context
-- to confirm the harness wiring works.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

-- Intentionally empty — slice 152's empty-state spec asserts behavior
-- when the tenant has zero instantiated controls. The bogus control
-- UUID the spec navigates to is `00000000-0000-0000-0000-000000000152`,
-- which by construction does not match any row.

COMMIT;
