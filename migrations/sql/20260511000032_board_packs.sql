-- security-atlas — quarterly board pack store (slice 032).
--
-- Implements docs/issues/032-quarterly-board-pack.md migration `_032`.
--
-- ----------------------------------------------------------------------------
-- The quarterly board pack (canvas §7.5) is the full board-meeting artifact:
-- posture summary, top-risks aging, open-findings, operator-entered
-- operational metrics, an investment-vs-coverage section, and an
-- asks-of-the-board section. Unlike the slice-031 monthly brief — which is
-- append-only and frozen at generation time — the quarterly pack has a
-- DRAFT -> PUBLISHED lifecycle: the operator generates a draft, reviews and
-- overrides the templated narrative section-by-section, approves each
-- section, and then publishes the pack as a frozen artifact.
--
-- This migration creates one table:
--
--   `board_packs` — a single table with a `status` column (`draft` |
--      `published`). A pack keeps a STABLE id across the draft -> publish
--      lifecycle. While `draft`, the `content` JSONB is mutable (section
--      overrides + per-section approval flags are atomic UPDATEs on the one
--      row). Once `published`, the row is IMMUTABLE.
--
-- Why a status column, not a new-row-on-publish chain (decision D1):
--
--   The `policies` slice models publication as a new immutable row chained to
--   its draft. The board pack instead follows the slice-028 `audit_periods`
--   FREEZE pattern: one row, a `status` column, and an UPDATE RLS policy
--   predicated on `status = 'draft'`. Once `status` flips to `published`, the
--   UPDATE policy's USING clause no longer matches the row, so atlas_app has
--   no SQL path to mutate it. The stable id means every artifact reference
--   (PDF URL, Markdown download, board-meeting minute) keeps resolving across
--   the lifecycle.
--
-- Immutability is enforced by TWO mechanisms, defense-in-depth (decision D1):
--
--   1. RLS UPDATE policy `USING (current_tenant_matches(tenant_id) AND
--      status = 'draft')`. A published row is invisible to UPDATE — the
--      primary guard.
--   2. A `BEFORE UPDATE` row trigger `board_packs_block_published_update`
--      that RAISEs an exception if `OLD.status = 'published'`. With RLS in
--      place this trigger is belt-and-suspenders (RLS already filters the row
--      out of the UPDATE), but it makes the invariant hold even for a
--      BYPASSRLS role (atlas_migrate) or a future policy regression. Mirrors
--      the slice-007 `framework_scopes_bounce_on_predicate_change` trigger
--      precedent: an UPDATE guard expressed as a BEFORE UPDATE trigger.
--
-- Why the full pack lives in one JSONB column (decision D2):
--
--   The structured pack — every section, including each section's
--   `templated_text`, `override_text`, and `approved` flag — is serialized
--   into `content` (JSONB). One row, atomic UPDATEs. Mirrors slice-031
--   `board_briefs.content`. The JSONB shape can evolve without a migration
--   while older packs keep their frozen content.
--
-- Constitutional invariants honored:
--
--   #6  Tenant isolation at the database layer. `board_packs` gets ENABLE +
--       FORCE ROW LEVEL SECURITY with the four-policy split
--       (tenant_read / tenant_write / tenant_update / tenant_delete) used by
--       slices 014/017/018/020/021/026/035/036/059. The tenant_update policy
--       additionally gates on `status = 'draft'` — the slice-028
--       `audit_periods` freeze precedent applied at the policy layer.
--
--   AI-assist boundary (CLAUDE.md): the pack narrative is templated only
--       (Go text/template over structured metrics — see internal/board/
--       pack_narrative.go). No LLM-generated text is written here. The
--       operator-entered sections (operational metrics, investment spend,
--       asks of the board) are human-authored. There is no `ai_assisted`
--       flag because no AI assistance path exists in this slice.
--
-- Anti-criteria honored at the schema layer (P0):
--   - No in-place mutation of a published pack: the tenant_update RLS policy
--     stops matching once status flips, and the BEFORE UPDATE trigger RAISEs
--     on OLD.status = 'published'.
--   - No auto-publish: there is no scheduled-write path; a pack is only
--     published because an operator POSTed /v1/board-packs/:id/publish, and
--     that handler rejects the publish unless every section is approved
--     (application-layer D6 gate).
--
-- Idempotency / reversibility:
--
--   A pure CREATE TABLE + CREATE FUNCTION + CREATE TRIGGER — no ALTER on a
--   pre-existing table, no enum types. Fully reversible via
--   20260511000032_board_packs.down.sql for a byte-clean up -> down -> up
--   round-trip.
-- ----------------------------------------------------------------------------

-- ===== board_packs =====
--
-- The quarterly board pack store. No FK to any framework / risk / control /
-- evaluation table: a published pack is a FROZEN SNAPSHOT, not a live
-- relation — its content must survive a later edit or delete of any entity
-- it summarized. The structured pack is captured into `content` at
-- generation time and frozen at publish time.

CREATE TABLE board_packs (
    id            UUID PRIMARY KEY,
    tenant_id     UUID NOT NULL,
    -- The quarter-end report date the pack is pinned to (YYYY-MM-DD). A DATE
    -- (not a timestamp): a board pack is a calendar-quarter artifact.
    -- Multiple packs may share a period_end — a re-generation is a NEW pack
    -- with a NEW id, never an edit of a published one — so this is
    -- intentionally NOT unique.
    period_end    DATE NOT NULL,
    -- Lifecycle state. `draft` packs are mutable (section overrides +
    -- approval flags). `published` packs are immutable (the tenant_update
    -- RLS policy stops matching + the BEFORE UPDATE trigger RAISEs).
    status        TEXT NOT NULL DEFAULT 'draft',
    -- The structured pack: posture summary, top-risks aging, open findings,
    -- operational metrics, investment-vs-coverage, asks of the board. Each
    -- section carries its own templated_text / override_text / approved
    -- flag. Serialized at generation time, mutated in place while draft,
    -- frozen at publish. JSONB so the structured shape can evolve without a
    -- migration while older packs keep their original content (decision D2).
    content       JSONB NOT NULL,
    -- The frozen rendered narrative (Markdown) — the board-ready
    -- paste-into-deck artifact. Produced by a Go text/template over
    -- `content` — NO LLM (P0 anti-criterion). Re-rendered on every content
    -- UPDATE while draft; the value at publish time is the frozen narrative.
    narrative_md  TEXT NOT NULL,
    -- Who published the pack, and when. NULL while draft; both set on the
    -- draft -> published transition (status-coherence CHECK below).
    published_by  TEXT NULL,
    published_at  TIMESTAMPTZ NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT board_packs_status_chk
        CHECK (status IN ('draft', 'published')),
    CONSTRAINT board_packs_narrative_nonempty
        CHECK (length(narrative_md) > 0),
    -- Status coherence: a published pack has both publish-metadata columns
    -- set; a draft pack has neither. The second line of defense behind the
    -- application-level Publish guard. Mirrors slice-020
    -- `audit_periods_frozen_coherent`.
    CONSTRAINT board_packs_published_coherent
        CHECK (
            (status = 'draft'
                AND published_at IS NULL
                AND published_by IS NULL)
            OR
            (status = 'published'
                AND published_at IS NOT NULL
                AND published_by IS NOT NULL
                AND length(published_by) > 0)
        )
);

-- The pack-list read: "every pack for this tenant, newest report-date
-- first". A tenant-prefixed B-tree over (period_end DESC, created_at DESC)
-- keeps the list query index-only.
CREATE INDEX idx_board_packs_tenant_period
    ON board_packs (tenant_id, period_end DESC, created_at DESC);

-- ===== BEFORE UPDATE trigger — published-pack immutability guard =====
--
-- Defense-in-depth behind the tenant_update RLS policy (decision D1). The
-- RLS policy's USING clause already filters a `published` row out of any
-- UPDATE for atlas_app, so this trigger is belt-and-suspenders for that
-- role. It earns its place by holding the invariant for a BYPASSRLS role
-- (atlas_migrate) and surviving a future RLS-policy regression: any UPDATE
-- touching a row whose OLD.status is already 'published' RAISEs.
--
-- The legitimate draft -> published transition (OLD.status = 'draft') flows
-- through unchanged. Mirrors the slice-007
-- framework_scopes_bounce_on_predicate_change trigger precedent.

CREATE OR REPLACE FUNCTION board_packs_block_published_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.status = 'published' THEN
        RAISE EXCEPTION
            'board_packs: row % is published and immutable', OLD.id
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER board_packs_block_published_update_trg
    BEFORE UPDATE ON board_packs
    FOR EACH ROW
    EXECUTE FUNCTION board_packs_block_published_update();

-- ===== Row-Level Security =====
--
-- Four-policy split, mirroring slices 014/017/018/020/021/026/035/036/059.
-- The tenant_update policy additionally gates on `status = 'draft'` — the
-- slice-028 audit_periods FREEZE precedent applied at the policy layer: once
-- status flips to 'published', the USING clause no longer matches the row,
-- so atlas_app has no SQL UPDATE path to a published pack (decision D1).

ALTER TABLE board_packs ENABLE ROW LEVEL SECURITY;
ALTER TABLE board_packs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON board_packs
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON board_packs
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON board_packs
    FOR UPDATE
    USING (current_tenant_matches(tenant_id) AND status = 'draft')
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON board_packs
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

-- ===== Grants =====
--
-- atlas_app gets SELECT + INSERT + UPDATE + DELETE. UPDATE is granted, but
-- the tenant_update policy's `status = 'draft'` predicate is what bounds it
-- to draft packs — the GRANT is necessary-but-not-sufficient. atlas_migrate
-- (BYPASSRLS) retains DDL access via role membership; the BEFORE UPDATE
-- trigger still guards published rows even for that role.
GRANT SELECT, INSERT, UPDATE, DELETE ON board_packs TO atlas_app;
