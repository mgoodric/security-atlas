-- Slice 082 — Playwright e2e seed for `web/e2e/control-detail.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The control-detail spec's preconditions (per its preamble):
--
--   - TEST_BEARER carries a credential in a tenant with at least one
--     control anchored to an SCF anchor with >=2 framework requirement
--     mappings
--   - at least one of those frameworks has an activated FrameworkScope
--     the control is OUT of (so the dashed/greyed OOS row has data)
--   - KNOWN_CONTROL_ID is that control's UUID
--
-- All inserts are ON CONFLICT DO NOTHING for idempotency.
--
-- NOTE: full schema satisfaction for AC-6 / AC-7 requires inserts into
-- scf_anchors + fw_to_scf_edges + framework_scopes. These tables are
-- platform-bundled (scf_anchors has no tenant_id) and the SCF anchors
-- referenced here use a synthetic framework_version so the e2e seed
-- does not collide with the bundled SCF catalog import. When the spec's
-- commented assertions are turned on (slice TBD; see slice 082
-- decisions log), this fixture grows to insert the synthetic anchor + 2
-- edges + 1 out-of-scope framework_scope. For the slice-082 harness
-- wiring this stub is enough — the spec's beforeAll calls succeed, the
-- assertions remain commented per the slice's scoping decision.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

-- The 00-seed.sql control (33333333-3333-3333-3333-333333330001) is
-- the KNOWN_CONTROL_ID for this spec. No extra rows needed for the
-- harness-wiring scope of slice 082.

COMMIT;
