-- security-atlas — slice 483: crosswalk-mapping verified-tier governance.
--
-- Implements ADR 0018 (docs/adr/0018-crosswalk-mapping-verified-tier.md). Adds
-- a TRUST dimension to the crosswalk mapping layer, ORTHOGONAL to the existing
-- `source_attribution` provenance field on fw_to_scf_edges (migration _013):
--
--   - source_attribution (provenance): WHERE did this mapping come from?
--       scf_official | community_draft | org_internal  (unchanged here).
--   - mapping_tier (trust):           HOW trusted is it NOW?
--       draft | under_review | verified | rejected     (this migration).
--
-- The two are NEVER collapsed (P0-483-3): an scf_official mapping can still be
-- re-reviewed, and a community_draft can become verified; both axes are needed.
--
-- Tier ladder (the state machine is enforced server-side in
-- internal/crosswalktier; this DDL only records the legal *value* set, not the
-- legal *transitions* — a CHECK can't model a state machine):
--
--   [draft] --claim--> [under_review] --verify--> [verified]
--      |                     |
--      +---------------------+--reject--> [rejected]   (terminal)
--
--   No skip-to-verified for a community draft (ADR 0018 §1); the seed step
--   below sets scf_official rows directly to verified (ADR 0018 §2).
--
-- # Catalog table — NOT tenant-scoped (deliberate; do NOT add tenant RLS)
--
-- fw_to_scf_edges is a BUNDLED CATALOG table (migration _013 header): no
-- tenant_id, no RLS. The tier is catalog-level reference data, not
-- tenant-confidential. The trust gate is therefore ADMIN-ROLE AUTHZ (the
-- transition handler in internal/api/admincrosswalktier requires cred.IsAdmin)
-- + an APPEND-ONLY audit table — NOT the four-policy tenant-RLS shape. The
-- audit table below mirrors the slice-013/036/509 append-only DISCIPLINE
-- (SELECT + INSERT grants only, no UPDATE/DELETE) but WITHOUT RLS, because it
-- is catalog-level (no tenant_id, no app.current_tenant dependency).
--
-- # Write mechanism (ADR 0018 Implementation notes; slice 483 decisions-log D1)
--
-- The API runs as atlas_app, which today holds only SELECT on fw_to_scf_edges.
-- We grant atlas_app a NARROW `UPDATE (mapping_tier)` column privilege so the
-- transition handler can flip the tier (and ONLY the tier — never the STRM
-- edge content) under its own role; the LEGALITY of the transition is enforced
-- in Go (the state machine) and the trust gate is the admin-role authz check.
-- atlas_app also gets SELECT + INSERT on the audit table. This avoids routing
-- the write through the privileged BYPASSRLS pool for a catalog-level edit.
--
-- Additive + reversible (P0-483-7): the column defaults to 'draft' for every
-- existing row, the scf_official seed step is a pure data UPDATE, and the down
-- migration drops the column + audit table + enum while leaving
-- source_attribution untouched.
--
-- Migration slot 20260612080000 (after slice 770's _070 action_plans).
-- Plain SQL (Atlas community caveat — no HCL row_security blocks).
-- Issue: docs/issues/483-crosswalk-mapping-verified-tier-governance.md
-- Reversible via 20260612080000_crosswalk_mapping_tier.down.sql.

-- ===== crosswalk_mapping_tier enum =====
--
-- Wrapped in DO/EXCEPTION for self-host re-run idempotency (slice 065 bug #3:
-- the docker-compose bundle re-applies every migration on each `up`, and
-- Postgres has no CREATE TYPE IF NOT EXISTS). The bare-enum twin lives in
-- internal/db/sqlc-schema/_enums.sql so sqlc v1.31.1 (which can't parse a
-- CREATE TYPE inside a DO block) emits a typed Go enum rather than interface{}.
DO $$ BEGIN
    CREATE TYPE crosswalk_mapping_tier AS ENUM (
        'draft',
        'under_review',
        'verified',
        'rejected'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- ===== mapping_tier column on fw_to_scf_edges (additive) =====

ALTER TABLE fw_to_scf_edges
    ADD COLUMN IF NOT EXISTS mapping_tier crosswalk_mapping_tier NOT NULL DEFAULT 'draft';

-- Seed policy (ADR 0018 §2): an scf_official crosswalk is a publisher's
-- official mapping — trusted on arrival, so it seeds directly at 'verified'.
-- This is a pure data UPDATE over existing rows; it touches ONLY the new
-- column and never rewrites source_attribution (P0-483-7). community_draft and
-- org_internal rows keep the column default 'draft'. Idempotent: re-running
-- this migration on a self-host `up` re-asserts the same value.
UPDATE fw_to_scf_edges
    SET mapping_tier = 'verified'
    WHERE source_attribution = 'scf_official'
      AND mapping_tier = 'draft';

-- Read hot path filters/labels by tier on /anchors + /coverage.
CREATE INDEX IF NOT EXISTS idx_fw_to_scf_edges_mapping_tier
    ON fw_to_scf_edges (mapping_tier);

-- ===== fw_to_scf_edge_tier_transitions (append-only audit) =====
--
-- One immutable row per tier transition (threat-model R / P0-483-4). Written
-- in the SAME transaction as the tier change (internal/crosswalktier.Store).
-- reviewer_id is the acting admin's atlas user id (the SubjectUserID from the
-- verified JWT). from_tier/to_tier record the move; note is the reviewer's
-- free-text rationale.
--
-- Append-only by construction: atlas_app is granted SELECT + INSERT ONLY (no
-- UPDATE/DELETE) below, so a transition row is immutable once written, exactly
-- like decision_audit_log (slice 035) / group_role_audit_log (slice 509) — but
-- WITHOUT RLS, because this is a catalog-level table with no tenant_id.
CREATE TABLE IF NOT EXISTS fw_to_scf_edge_tier_transitions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    edge_id     UUID NOT NULL REFERENCES fw_to_scf_edges(id) ON DELETE CASCADE,
    reviewer_id UUID NOT NULL,
    from_tier   crosswalk_mapping_tier NOT NULL,
    to_tier     crosswalk_mapping_tier NOT NULL,
    note        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_fw_to_scf_edge_tier_transitions_edge
    ON fw_to_scf_edge_tier_transitions (edge_id, created_at DESC);

-- ===== grants =====
--
-- fw_to_scf_edges already grants SELECT to atlas_app (migration _013). Add the
-- NARROW column-level UPDATE (mapping_tier, updated_at) so the transition
-- handler can flip the trust tier AND stamp updated_at under the app role; it
-- still cannot touch relationship_type, strength, source_attribution, or
-- rationale (the STRM edge content) through this grant (write mechanism (a),
-- slice 483 D1). updated_at MUST be in the grant because the tier-update query
-- writes `updated_at = now()` alongside the tier, and Postgres column-level
-- UPDATE privilege is checked per-written-column. atlas_migrate keeps full
-- DDL/import writes.
GRANT UPDATE (mapping_tier, updated_at) ON fw_to_scf_edges TO atlas_app;

-- Audit table: append-only — SELECT + INSERT to atlas_app, NO UPDATE/DELETE.
GRANT SELECT, INSERT ON fw_to_scf_edge_tier_transitions TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON fw_to_scf_edge_tier_transitions TO atlas_migrate;
