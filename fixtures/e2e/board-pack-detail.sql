-- Slice 218 — Playwright e2e seed for `web/e2e/board-pack-detail.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The board-pack-detail spec's preconditions (per its preamble):
--
--   - TEST_BEARER carries a credential in a tenant with at least one
--     board pack (status=draft or status=published)
--   - KNOWN_BOARD_PACK_ID is that pack's UUID
--   - KNOWN_BOARD_PACK_PERIOD_END is its YYYY-MM-DD period_end
--
-- All test bodies in the spec are currently commented (quarantined per
-- slice 082 pattern), so this fixture only needs to satisfy the
-- harness-wiring contract — seedFromFixture must find a file at this
-- path. When the spec's commented assertions are turned on, this file
-- grows to insert a synthetic board_pack row with a calendar-quarter
-- period_end UUID matching KNOWN_BOARD_PACK_ID.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

-- Harness-wiring stub. No rows inserted; spec assertions remain
-- commented per slice 082 quarantine pattern.

COMMIT;
