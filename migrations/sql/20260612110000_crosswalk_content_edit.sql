-- security-atlas — slice 536b: crosswalk mapping CONTENT editing + its audit trail.
--
-- Slice 483 (migration 20260612080000) shipped the TRUST dimension: mapping_tier
-- plus fw_to_scf_edge_tier_transitions. It deliberately withheld UPDATE on the
-- STRM content columns from atlas_app (483 D1) — the tier grant is
-- `UPDATE (mapping_tier, updated_at)` and nothing more, so the reviewer could
-- promote a mapping but never fix one.
--
-- Slice 536a's scope reconciliation (docs/audit-log/536a-...-decisions.md §1.4
-- residual #2) names exactly that gap as 536b's only new write surface: content
-- editing of relationship_type / strength / rationale, with its own audit trail.
-- This migration is that surface.
--
-- # The grant widening is the privilege expansion — read D-536b-2 first
--
-- 483 D1 chose a narrow column grant on purpose. Widening it is a real
-- expansion, so the new grant is still deliberately NARROW in the two ways that
-- carry the invariants:
--
--   * framework_requirement_id and scf_anchor_id are NOT granted. atlas_app
--     therefore cannot re-point an edge at a different requirement or anchor
--     through any code path. Constitutional invariant #7 (mappings go
--     requirement -> SCF anchor, never requirement -> requirement) is held by
--     the GRANT itself, not only by the handler: no reviewer edit can change
--     the shape of the graph, only the semantics of an existing edge.
--   * source_attribution is NOT granted. ADR 0018 / 483 P0-483-3 made
--     provenance and trust orthogonal: source_attribution records WHERE a
--     mapping came from and is import-time history. Promoting it would falsify
--     that history, which is precisely the obsolete approval model slice 536
--     was originally written around (536a §1.2).
--
-- What IS granted is relationship_type, strength and rationale — the three
-- fields the slice-536 doc names as reviewer-editable, plus updated_at (a
-- column-level UPDATE privilege is checked per written column and the edit
-- query stamps updated_at alongside).
--
-- # Repudiation (threat-model R)
--
-- fw_to_scf_edge_content_edits is append-only by GRANT (SELECT + INSERT, no
-- UPDATE/DELETE) and is written in the SAME transaction as the content change
-- by internal/crosswalkedit.Store — the same discipline 483 P0-483-4 applied to
-- tier transitions, and the same shape as decision_audit_log (slice 035) /
-- group_role_audit_log (slice 509). It records the full before/after of all
-- three columns, so an edit is reconstructible from the trail alone.
--
-- Catalog-level, so NO RLS: fw_to_scf_edges has no tenant_id (migration _013
-- header). The gate is admin-role authz at the handler
-- (internal/api/admincrosswalkreview) plus this append-only trail — never the
-- four-policy tenant-RLS shape.
--
-- Additive + reversible: one new table, one widened grant. The down migration
-- drops the table and re-narrows the grant back to 483's
-- `UPDATE (mapping_tier, updated_at)`.
--
-- Migration slot 20260612110000 (after slice 493's _100 tenant_llm_routing).
-- Plain SQL (Atlas community caveat — no HCL row_security blocks).
-- Issue: docs/issues/536-crosswalk-review-conflict-editing-ui.md

-- ===== fw_to_scf_edge_content_edits (append-only audit) =====
--
-- One immutable row per accepted content edit. editor_id is the acting admin's
-- atlas user id (the SubjectUserID from the verified JWT) — the same identity
-- source fw_to_scf_edge_tier_transitions.reviewer_id uses, never a request-body
-- field. note is the editor's free-text rationale for the change itself, which
-- is distinct from the edge's own `rationale` column (that is the mapping's
-- justification; this is why the mapping changed).
CREATE TABLE IF NOT EXISTS fw_to_scf_edge_content_edits (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    edge_id                UUID NOT NULL REFERENCES fw_to_scf_edges(id) ON DELETE CASCADE,
    editor_id              UUID NOT NULL,
    from_relationship_type strm_relationship_type NOT NULL,
    to_relationship_type   strm_relationship_type NOT NULL,
    from_strength          DOUBLE PRECISION NOT NULL,
    to_strength            DOUBLE PRECISION NOT NULL,
    from_rationale         TEXT NOT NULL DEFAULT '',
    to_rationale           TEXT NOT NULL DEFAULT '',
    note                   TEXT NOT NULL DEFAULT '',
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- The same [0,1] bound fw_to_scf_edges_strength_range puts on the live
    -- column. An audit row that records an out-of-range strength would be
    -- recording something the edge itself could never have held.
    CONSTRAINT fw_to_scf_edge_content_edits_strength_range
        CHECK (from_strength >= 0.0 AND from_strength <= 1.0
           AND to_strength   >= 0.0 AND to_strength   <= 1.0)
);

CREATE INDEX IF NOT EXISTS idx_fw_to_scf_edge_content_edits_edge
    ON fw_to_scf_edge_content_edits (edge_id, created_at DESC);

-- ===== grants =====
--
-- Widen the 483 D1 column grant to the three STRM content columns. See the
-- header: framework_requirement_id / scf_anchor_id / source_attribution stay
-- OUT of the grant, which is what keeps invariant #7 and the provenance axis
-- enforced at the database rather than only in Go. atlas_migrate keeps full
-- DDL/import writes (migration _013).
GRANT UPDATE (mapping_tier, relationship_type, strength, rationale, updated_at)
    ON fw_to_scf_edges TO atlas_app;

-- Audit table: append-only — SELECT + INSERT to atlas_app, NO UPDATE/DELETE.
GRANT SELECT, INSERT ON fw_to_scf_edge_content_edits TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON fw_to_scf_edge_content_edits TO atlas_migrate;
