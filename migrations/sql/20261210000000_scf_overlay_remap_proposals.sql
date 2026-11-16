-- security-atlas — #1804: tenant overlay edges join the SCF remap queue.
--
-- The third subject type of the remap queue, after controls (#1775, #1776)
-- and catalog crosswalk edges (#1777). When `atlas-cli catalog import-scf`
-- makes a new SCF release current, every ACTIVE fw_to_scf_overlay_edges row
-- (#1735) still on an anchor of an older release gets one proposal here,
-- classified exactly as a control or catalog-edge proposal is (unchanged,
-- renamed, split, reused_id, no_successor) from the new release's legacy
-- IDs.
--
-- Overlay edges are tenant-owned, so this queue is too, and its shape
-- follows scf_remap_proposals rather than #1777's global table:
--
--   * tenant_id on every row, ENABLE + FORCE RLS, tenant policies with
--     WITH CHECK, composite (tenant_id, ...) FKs (#2336), so a proposal or
--     decision can only ever name rows of its own tenant.
--   * A tenant writer (admin, grc_engineer) decides, not a super-admin. The
--     decision runs as atlas_app under the caller's app.current_tenant and
--     RLS; there is no SECURITY DEFINER function. The app role holds SELECT
--     and UPDATE on proposals and SELECT and INSERT on decisions, and
--     already holds the overlay table's INSERT and UPDATE.
--   * The import queues every tenant's edges at once, a global act. It runs
--     as the migrate role (BYPASSRLS), as QueueRemapProposals and
--     QueueEdgeRemapProposals do, and each proposal's tenant_id is read from
--     its overlay edge; the composite FK refuses any other value.
--
-- A writer resolves a proposal in one of three ways, as for controls:
--
--   approved     a new overlay edge is created on the chosen anchor in the
--                target release, in the same tenant, with the same
--                requirement, relationship, strength, rationale and
--                source_ref; the legacy overlay edge is retired with reason
--                'remapped'. Both changes write the overlay audit trail.
--                Final.
--   rejected     the edge stays on its legacy anchor; revisitable
--   kept_legacy  the same, final
--
-- History cascades from the overlay edges and anchors, like #1777's: an
-- overlay edge is never deleted except by a cascade from its catalog
-- requirement or anchor, which in practice only a test wiping the catalog
-- does.

-- The composite FK target on the overlay table.
DO $$
BEGIN
    ALTER TABLE fw_to_scf_overlay_edges
        ADD CONSTRAINT fw_to_scf_overlay_edges_tenant_id_uniq UNIQUE (tenant_id, id);
EXCEPTION WHEN duplicate_object OR duplicate_table THEN NULL;
END $$;

CREATE TABLE IF NOT EXISTS scf_overlay_remap_proposals (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id                   UUID NOT NULL,
    overlay_edge_id             UUID NOT NULL,
    legacy_anchor_id            UUID NOT NULL REFERENCES scf_anchors(id) ON DELETE CASCADE,
    legacy_scf_id               TEXT NOT NULL,
    legacy_release              TEXT NOT NULL,
    target_framework_version_id UUID NOT NULL REFERENCES framework_versions(id) ON DELETE CASCADE,
    target_release              TEXT NOT NULL,
    class                       TEXT NOT NULL
                                CONSTRAINT scf_overlay_remap_proposals_class_check
                                CHECK (class IN ('unchanged', 'renamed', 'split', 'reused_id', 'no_successor')),
    status                      TEXT NOT NULL DEFAULT 'open'
                                CONSTRAINT scf_overlay_remap_proposals_status_check
                                CHECK (status IN ('open', 'approved', 'rejected', 'kept_legacy')),
    -- The proposed successor anchors in the target release, by SCF code.
    successor_scf_ids           TEXT[] NOT NULL DEFAULT '{}',
    -- Set on approval: the anchor chosen and the overlay edge created on it.
    target_anchor_id            UUID REFERENCES scf_anchors(id) ON DELETE CASCADE,
    new_overlay_edge_id         UUID,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT scf_overlay_remap_proposals_tenant_id_uniq UNIQUE (tenant_id, id),
    CONSTRAINT scf_overlay_remap_proposals_overlay_edge_fk
        FOREIGN KEY (tenant_id, overlay_edge_id)
        REFERENCES fw_to_scf_overlay_edges (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT scf_overlay_remap_proposals_new_overlay_edge_fk
        FOREIGN KEY (tenant_id, new_overlay_edge_id)
        REFERENCES fw_to_scf_overlay_edges (tenant_id, id) ON DELETE CASCADE,
    -- One proposal per overlay edge per target release; a re-run of the
    -- same import leaves the existing row alone.
    CONSTRAINT scf_overlay_remap_proposals_edge_target_uniq
        UNIQUE (overlay_edge_id, target_framework_version_id),
    CONSTRAINT scf_overlay_remap_proposals_successors_match_class CHECK (
        CASE class
            WHEN 'no_successor' THEN cardinality(successor_scf_ids) = 0
            WHEN 'split'        THEN cardinality(successor_scf_ids) > 1
            WHEN 'reused_id'    THEN true
            ELSE cardinality(successor_scf_ids) = 1
        END),
    CONSTRAINT scf_overlay_remap_proposals_target_matches_status CHECK (
        (status = 'approved') = (target_anchor_id IS NOT NULL)
        AND (status = 'approved') = (new_overlay_edge_id IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS idx_scf_overlay_remap_proposals_tenant_class_status
    ON scf_overlay_remap_proposals (tenant_id, class, status);

ALTER TABLE scf_overlay_remap_proposals ENABLE ROW LEVEL SECURITY;
ALTER TABLE scf_overlay_remap_proposals FORCE ROW LEVEL SECURITY;

DO $$
BEGIN
    CREATE POLICY tenant_read ON scf_overlay_remap_proposals
        FOR SELECT USING (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    CREATE POLICY tenant_update ON scf_overlay_remap_proposals
        FOR UPDATE
        USING (current_tenant_matches(tenant_id))
        WITH CHECK (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- The app role decides; it never queues (the import does) or deletes.
GRANT SELECT, UPDATE ON scf_overlay_remap_proposals TO atlas_app;

CREATE TABLE IF NOT EXISTS scf_overlay_remap_decisions (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id              UUID NOT NULL,
    proposal_id            UUID NOT NULL,
    decision               TEXT NOT NULL
                           CONSTRAINT scf_overlay_remap_decisions_decision_check
                           CHECK (decision IN ('approved', 'rejected', 'kept_legacy')),
    legacy_overlay_edge_id UUID NOT NULL,
    legacy_anchor_id       UUID NOT NULL REFERENCES scf_anchors(id) ON DELETE CASCADE,
    legacy_scf_id          TEXT NOT NULL,
    legacy_release         TEXT NOT NULL,
    -- Set on an approval only.
    new_overlay_edge_id    UUID,
    new_anchor_id          UUID REFERENCES scf_anchors(id) ON DELETE CASCADE,
    new_scf_id             TEXT,
    new_release            TEXT,
    reason                 TEXT CHECK (reason IS NULL OR char_length(reason) <= 2000),
    -- PrincipalOf the deciding person (a users(id) uuid).
    decided_by             TEXT NOT NULL CHECK (btrim(decided_by) <> ''),
    decided_at             TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT scf_overlay_remap_decisions_proposal_fk
        FOREIGN KEY (tenant_id, proposal_id)
        REFERENCES scf_overlay_remap_proposals (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT scf_overlay_remap_decisions_legacy_edge_fk
        FOREIGN KEY (tenant_id, legacy_overlay_edge_id)
        REFERENCES fw_to_scf_overlay_edges (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT scf_overlay_remap_decisions_new_edge_fk
        FOREIGN KEY (tenant_id, new_overlay_edge_id)
        REFERENCES fw_to_scf_overlay_edges (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT scf_overlay_remap_decisions_target_matches_decision CHECK (
        (decision = 'approved') = (new_overlay_edge_id IS NOT NULL)
        AND (decision = 'approved') = (new_anchor_id IS NOT NULL)
        AND (decision = 'approved') = (new_scf_id IS NOT NULL)
        AND (decision = 'approved') = (new_release IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS idx_scf_overlay_remap_decisions_tenant_proposal
    ON scf_overlay_remap_decisions (tenant_id, proposal_id, decided_at);

ALTER TABLE scf_overlay_remap_decisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE scf_overlay_remap_decisions FORCE ROW LEVEL SECURITY;

DO $$
BEGIN
    CREATE POLICY tenant_read ON scf_overlay_remap_decisions
        FOR SELECT USING (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
    CREATE POLICY tenant_insert ON scf_overlay_remap_decisions
        FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Append-only for the app role.
GRANT SELECT, INSERT ON scf_overlay_remap_decisions TO atlas_app;
