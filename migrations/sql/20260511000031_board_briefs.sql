-- security-atlas — monthly board brief pinned-snapshot store (slice 031).
--
-- Implements docs/issues/031-monthly-board-brief.md migration `_031`.
--
-- ----------------------------------------------------------------------------
-- The monthly board brief (canvas §7.5) is a single-page, board-ready posture
-- snapshot: per-framework posture, drift in the last 30 days, top-3 risks
-- aging. The defining property is that it is a PINNED SNAPSHOT — the board
-- reads what posture WAS at the report date even if live state changes
-- afterward (canvas §7.5 "Both are pinned snapshots").
--
-- This migration creates one table:
--
--   `board_briefs` — an append-only store of generated briefs. Every
--      generation APPENDS one row. The structured metrics are frozen into
--      `content` (JSONB) and the rendered narrative into `narrative_md`
--      (TEXT) at generation time. Re-fetching a brief reads those frozen
--      columns verbatim — AC-5 (the snapshot is immutable).
--
-- Why append-only, not UPSERT or mutable:
--
--   AC-5 + the P0 anti-criterion "Does NOT permit edit of a pinned snapshot —
--   new brief = new snapshot" make immutability a hard requirement. The table
--   is modelled append-only by construction: it gets SELECT + INSERT RLS
--   policies ONLY. Under FORCE ROW LEVEL SECURITY the explicit ABSENCE of
--   UPDATE/DELETE policies means atlas_app has no path to mutate a brief row.
--   Immutability is STRUCTURAL, not application-enforced — the same pattern
--   slice 013's `evidence_audit_log`, slice 026's
--   `aggregation_rule_evaluations`, slice 030's `decisions_audit`, and slice
--   036's `artifact_access_log` use. A second POST with the same period_end
--   produces a NEW row with a NEW id (AC anti-criterion), never an edit.
--
-- Why the PDF is not stored:
--
--   The brief renders deterministically from `content` + `narrative_md`. The
--   PDF endpoint re-renders on demand via the existing chromedp path. Storing
--   PDF bytes would bloat the row for no correctness gain — the frozen JSONB
--   IS the snapshot; the PDF is just a presentation of it.
--
-- Constitutional invariants honored:
--
--   #6  Tenant isolation at the database layer. `board_briefs` gets ENABLE +
--       FORCE ROW LEVEL SECURITY with SELECT + INSERT policies only — the
--       explicit absence of UPDATE/DELETE policies under FORCE makes the
--       table append-only by construction (same as slice 030's
--       `decisions_audit`).
--
--   AI-assist boundary (CLAUDE.md): the brief narrative is templated only
--       (Go text/template over structured metrics). No LLM-generated text is
--       written here. The `narrative_md` column holds deterministically
--       rendered template output — there is no `ai_assisted` flag because
--       no AI assistance path exists in this slice.
--
-- Anti-criteria honored at the schema layer (P0):
--   - No edit of a pinned snapshot: no UPDATE/DELETE policy under FORCE RLS.
--   - No auto-publish: there is no scheduled-write path; a brief row only
--     exists because an operator POSTed /v1/board-briefs (application-layer
--     invariant).
--
-- Idempotency / reversibility:
--
--   A pure CREATE TABLE — no ALTER on a pre-existing table, no enum types.
--   Fully reversible via 20260511000031_board_briefs.down.sql for a
--   byte-clean up -> down -> up round-trip.
-- ----------------------------------------------------------------------------

-- ===== board_briefs =====
--
-- Append-only store of generated monthly board briefs. No FK to any
-- framework / risk / control table: the brief is a FROZEN SNAPSHOT, not a
-- live relation — its content must survive a later edit or delete of any
-- entity it summarized. The structured metrics are captured verbatim into
-- `content` at generation time.

CREATE TABLE board_briefs (
    id            UUID PRIMARY KEY,
    tenant_id     UUID NOT NULL,
    -- The report date the brief is pinned to. A DATE (not a timestamp): a
    -- board brief is a calendar-month artifact. Multiple briefs may share a
    -- period_end — a re-generation is a NEW row, never an edit (AC
    -- anti-criterion), so this is intentionally NOT unique.
    period_end    DATE NOT NULL,
    -- Wall-clock stamp of the generation that produced this brief.
    generated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- The frozen structured brief: per-framework posture rows, the 30-day
    -- drift count, and the top-3 risks aging. Serialized at generation time
    -- and read back verbatim — AC-5 (the snapshot is immutable). JSONB so the
    -- structured shape can evolve without a migration while older briefs keep
    -- their original frozen content.
    content       JSONB NOT NULL,
    -- The frozen rendered narrative (Markdown). Produced by a Go text/template
    -- over `content` at generation time — NO LLM (AC-6, P0 anti-criterion).
    -- Frozen here so a re-fetch returns byte-identical narrative even if the
    -- template later changes.
    narrative_md  TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT board_briefs_narrative_nonempty
        CHECK (length(narrative_md) > 0)
);

-- The brief-list read: "every brief for this tenant, newest report-date
-- first". A tenant-prefixed B-tree over (period_end DESC, generated_at DESC)
-- keeps the list query and the latest-per-period lookup index-only.
CREATE INDEX idx_board_briefs_tenant_period
    ON board_briefs (tenant_id, period_end DESC, generated_at DESC);

-- ===== Row-Level Security =====
--
-- board_briefs is append-only by construction: SELECT + INSERT policies
-- only. No UPDATE/DELETE policy under FORCE ROW LEVEL SECURITY means
-- atlas_app cannot mutate a brief row — AC-5 (immutable snapshot) and the P0
-- anti-criterion "Does NOT permit edit of a pinned snapshot" hold
-- structurally. Mirrors slice 013's evidence_audit_log, slice 026's
-- aggregation_rule_evaluations, slice 030's decisions_audit, slice 036's
-- artifact_access_log.

ALTER TABLE board_briefs ENABLE ROW LEVEL SECURITY;
ALTER TABLE board_briefs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON board_briefs
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON board_briefs
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));

-- ===== Grants =====
--
-- atlas_app gets SELECT + INSERT on the append-only table. atlas_migrate
-- (BYPASSRLS) retains DDL access via role membership. The ABSENCE of an
-- UPDATE/DELETE policy — not the GRANT — is what makes the table append-only
-- for atlas_app.
GRANT SELECT, INSERT ON board_briefs TO atlas_app;
