-- security-atlas — slice 536b-1: crosswalk-edge CONTENT editing + its audit trail.
--
-- Slice 483 (migration 20260612080000) shipped the TRUST dimension: the
-- mapping_tier state machine, its transition endpoint, and the append-only
-- fw_to_scf_edge_tier_transitions trail. It deliberately withheld UPDATE on
-- the STRM content columns from atlas_app (slice 483 decisions-log D1): the
-- app role could flip the tier but never rewrite what a mapping SAYS.
--
-- This migration is the deliberate, threat-modelled widening of that grant
-- (536a decisions-log D-536b-2): the crosswalk-review surface lets an admin
-- reviewer curate a community_draft mapping's relationship_type / strength /
-- rationale in-product instead of hand-editing data/crosswalks/*.yaml. The
-- widening is bounded three ways:
--
--   1. COLUMN-scoped: atlas_app gains UPDATE on relationship_type, strength,
--      rationale ONLY. source_attribution (import-time provenance — rewriting
--      it would falsify history), framework_requirement_id and scf_anchor_id
--      (the edge ENDPOINTS — re-pointing an edge is a delete+create import
--      act, and immutable endpoints keep invariant #7's requirement -> SCF
--      anchor shape unforgeable through this surface) stay import-owned.
--   2. AUDITED: every content edit appends an immutable before/after row to
--      fw_to_scf_edge_content_edits in the SAME transaction as the UPDATE
--      (internal/crosswalkedit.Store) — the content twin of 483's
--      tier-transition trail (threat-model R: mapping edits move coverage
--      scores, so they are an auditable change to catalog semantics).
--   3. TIER-gated in Go: the store refuses content edits on verified /
--      rejected edges (a verified mapping's content is exactly what was
--      verified), so the reviewer demotes via 483's state machine first.
--
-- # Catalog table — NOT tenant-scoped (deliberate; do NOT add tenant RLS)
--
-- fw_to_scf_edges is a BUNDLED CATALOG table (migration _013 header): no
-- tenant_id, no RLS. Same for this audit table: the gate is ADMIN-ROLE AUTHZ
-- (the edit handler in internal/api/admincrosswalkreview requires
-- cred.IsAdmin) + append-only GRANT discipline (SELECT + INSERT only, no
-- UPDATE/DELETE), exactly like fw_to_scf_edge_tier_transitions.
--
-- Additive + reversible: a new table + a column-level GRANT; no existing
-- column or row is touched. The down migration drops the table and revokes
-- the widened grant, restoring slice 483's narrow posture.
--
-- Migration slot 20260804000000 (after 20260802000000_change_management).
-- Plain SQL (Atlas community caveat — no HCL row_security blocks).
-- Issue: docs/issues/536-crosswalk-review-conflict-editing-ui.md (536b-1).
-- Reversible via 20260804000000_crosswalk_edge_content_edits.down.sql.

-- ===== fw_to_scf_edge_content_edits (append-only audit) =====
--
-- One immutable row per content edit, with the full before/after diff
-- (threat-model R: "every edit ... logged with the actor, the before/after
-- diff, and timestamp"). editor_id is the acting admin's atlas user id (the
-- SubjectUserID from the verified JWT — taken from the session, never the
-- request body). note is the editor's free-text rationale for the edit.
CREATE TABLE IF NOT EXISTS fw_to_scf_edge_content_edits (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    edge_id                 UUID NOT NULL REFERENCES fw_to_scf_edges(id) ON DELETE CASCADE,
    editor_id               UUID NOT NULL,
    from_relationship_type  strm_relationship_type NOT NULL,
    to_relationship_type    strm_relationship_type NOT NULL,
    from_strength           DOUBLE PRECISION NOT NULL,
    to_strength             DOUBLE PRECISION NOT NULL,
    from_rationale          TEXT NOT NULL,
    to_rationale            TEXT NOT NULL,
    note                    TEXT NOT NULL DEFAULT '',
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_fw_to_scf_edge_content_edits_edge
    ON fw_to_scf_edge_content_edits (edge_id, created_at DESC);

-- ===== grants =====
--
-- The D-536b-2 widening: atlas_app may now rewrite the three
-- reviewer-curated content columns. Postgres column-level UPDATE privilege
-- is checked per-written-column, so this composes with (does not replace)
-- slice 483's UPDATE (mapping_tier, updated_at) grant — the edit query
-- writes updated_at through the existing grant. source_attribution and the
-- edge-endpoint FKs remain excluded: atlas_app cannot rewrite provenance or
-- re-point an edge through any granted path.
GRANT UPDATE (relationship_type, strength, rationale) ON fw_to_scf_edges TO atlas_app;

-- Audit table: append-only — SELECT + INSERT to atlas_app, NO UPDATE/DELETE.
GRANT SELECT, INSERT ON fw_to_scf_edge_content_edits TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON fw_to_scf_edge_content_edits TO atlas_migrate;
