-- Slice 218 — Playwright e2e seed for `web/e2e/board-pack-detail.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). Inserts one draft board pack in the demo tenant with a
-- calendar-quarter period_end, so the detail page's breadcrumb renders
-- its trailing segment as a `Q1 2026` label rather than the raw date.
--
-- The id is mirrored as PACK_ID in the spec. Idempotent (ON CONFLICT DO
-- NOTHING); the spec only reads the pack, so a re-run sees the same row.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

INSERT INTO board_packs (id, tenant_id, period_end, status, content, narrative_md)
VALUES (
    '21800000-0000-4000-8000-000000000218',
    '00000000-0000-0000-0000-00000000d3a0',
    '2026-03-31',
    'draft',
    jsonb_build_object(
        'period_end', '2026-03-31',
        'generated_at', '2026-04-01T00:00:00Z',
        'status', 'draft',
        'sections', '{}'::jsonb
    ),
    'Board pack detail e2e pack (slice 218).'
)
ON CONFLICT (id) DO NOTHING;

COMMIT;
