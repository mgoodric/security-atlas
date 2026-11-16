-- security-atlas — #1777: crosswalk-edge remap proposals.
--
-- The control remap queue (#1775, #1776) has a global counterpart for the
-- catalog's crosswalk edges. When `atlas-cli catalog import-scf` makes a new
-- SCF release current, every live fw_to_scf_edges row still on an anchor of
-- an older release gets one proposal here, classified exactly as a control
-- proposal is (unchanged, renamed, split, reused_id, no_successor) from the
-- new release's legacy IDs.
--
-- fw_to_scf_edges is a global catalog table, shared by every tenant, so
-- these proposals carry no tenant_id and no RLS. Only a super-admin decides
-- them (#1757). The database cannot see that claim, so the gate is the
-- handler plus OPA; what the database enforces is that the app role can
-- change nothing here except through scf_edge_remap_decide(), which validates
-- the decision and appends it to the history in one statement. The app role
-- holds SELECT on both tables and EXECUTE on that function and on the bulk
-- lock helper scf_edge_remap_lock_open_unchanged(); no INSERT,
-- UPDATE or DELETE on either table, and no INSERT on fw_to_scf_edges.
--
-- A reviewer resolves a proposal in one of three ways, as for controls:
--
--   approved     a copy of the edge is created on the chosen anchor in the
--                target release, with the same requirement, relationship,
--                strength, rationale, source attribution and mapping tier;
--                final. The legacy edge is left exactly as it was: it is
--                the history, and the default coverage read already pins
--                to the current release (#2203), so it stops counting
--                there without being retired.
--   rejected     the edge stays on its legacy anchor only; revisitable
--   kept_legacy  the same, final
--
-- Tenant overlay edges (#1735) are not queued here; they are tenant-scoped
-- and follow in #2360. That table would key on tenant_id with RLS, like
-- scf_remap_proposals, rather than widen this one.
--
-- History survives as long as the catalog rows it describes: proposals and
-- decisions cascade from the legacy edge and anchors. The catalog retires
-- edges rather than deleting them (#1971) and freezes an imported release's
-- anchors, so in practice only a test wiping the catalog removes them.

CREATE TABLE IF NOT EXISTS scf_edge_remap_proposals (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The requirement is the edge's own; it is not repeated here, because
    -- fw_to_scf_edges stays the only catalog bridge into
    -- framework_requirements (invariant 1, guarded by soc2import's
    -- TestImport_NoDirectRequirementToRequirementTableExists).
    edge_id                     UUID NOT NULL REFERENCES fw_to_scf_edges(id) ON DELETE CASCADE,
    legacy_anchor_id            UUID NOT NULL REFERENCES scf_anchors(id) ON DELETE CASCADE,
    legacy_scf_id               TEXT NOT NULL,
    legacy_release              TEXT NOT NULL,
    target_framework_version_id UUID NOT NULL REFERENCES framework_versions(id) ON DELETE CASCADE,
    target_release              TEXT NOT NULL,
    class                       TEXT NOT NULL
                                CONSTRAINT scf_edge_remap_proposals_class_check
                                CHECK (class IN ('unchanged', 'renamed', 'split', 'reused_id', 'no_successor')),
    status                      TEXT NOT NULL DEFAULT 'open'
                                CONSTRAINT scf_edge_remap_proposals_status_check
                                CHECK (status IN ('open', 'approved', 'rejected', 'kept_legacy')),
    -- The proposed successor anchors in the target release, by SCF code.
    successor_scf_ids           TEXT[] NOT NULL DEFAULT '{}',
    -- Set on approval: the anchor chosen and the edge created on it.
    target_anchor_id            UUID REFERENCES scf_anchors(id) ON DELETE CASCADE,
    new_edge_id                 UUID REFERENCES fw_to_scf_edges(id) ON DELETE CASCADE,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- One proposal per edge per target release; a re-run of the same import
    -- leaves the existing row alone.
    CONSTRAINT scf_edge_remap_proposals_edge_target_uniq
        UNIQUE (edge_id, target_framework_version_id),
    CONSTRAINT scf_edge_remap_proposals_successors_match_class CHECK (
        CASE class
            WHEN 'no_successor' THEN cardinality(successor_scf_ids) = 0
            WHEN 'split'        THEN cardinality(successor_scf_ids) > 1
            WHEN 'reused_id'    THEN true
            ELSE cardinality(successor_scf_ids) = 1
        END),
    CONSTRAINT scf_edge_remap_proposals_target_matches_status CHECK (
        (status = 'approved') = (target_anchor_id IS NOT NULL)
        AND (status = 'approved') = (new_edge_id IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS idx_scf_edge_remap_proposals_class_status
    ON scf_edge_remap_proposals (class, status);

CREATE TABLE IF NOT EXISTS scf_edge_remap_decisions (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    proposal_id      UUID NOT NULL REFERENCES scf_edge_remap_proposals(id) ON DELETE CASCADE,
    decision         TEXT NOT NULL
                     CONSTRAINT scf_edge_remap_decisions_decision_check
                     CHECK (decision IN ('approved', 'rejected', 'kept_legacy')),
    -- True when the decision came from bulk-approve of unchanged proposals.
    bulk             BOOLEAN NOT NULL DEFAULT false,
    legacy_edge_id   UUID NOT NULL REFERENCES fw_to_scf_edges(id) ON DELETE CASCADE,
    legacy_anchor_id UUID NOT NULL REFERENCES scf_anchors(id) ON DELETE CASCADE,
    legacy_scf_id    TEXT NOT NULL,
    legacy_release   TEXT NOT NULL,
    -- Set on an approval only.
    new_edge_id      UUID REFERENCES fw_to_scf_edges(id) ON DELETE CASCADE,
    new_anchor_id    UUID REFERENCES scf_anchors(id) ON DELETE CASCADE,
    new_scf_id       TEXT,
    new_release      TEXT,
    reason           TEXT CHECK (reason IS NULL OR char_length(reason) <= 2000),
    -- PrincipalOf the deciding super-admin (a users(id) uuid).
    decided_by       TEXT NOT NULL CHECK (btrim(decided_by) <> ''),
    decided_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT scf_edge_remap_decisions_target_matches_decision CHECK (
        (decision = 'approved') = (new_edge_id IS NOT NULL)
        AND (decision = 'approved') = (new_anchor_id IS NOT NULL)
        AND (decision = 'approved') = (new_scf_id IS NOT NULL)
        AND (decision = 'approved') = (new_release IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS idx_scf_edge_remap_decisions_proposal
    ON scf_edge_remap_decisions (proposal_id, decided_at);

-- scf_edge_remap_decide applies one decision. It locks the proposal, checks
-- it can still be decided, and for an approval checks the legacy edge is
-- unchanged and live, resolves the chosen code in the target release, and
-- refuses when the requirement already has an edge on that anchor. Errors
-- carry a stable prefix the store maps to HTTP statuses:
--
--   edge-remap: not found         unknown proposal
--   edge-remap: decided           status does not admit this decision
--   edge-remap: anchor required   no single successor and no code given
--   edge-remap: anchor not in release
--   edge-remap: edge changed      legacy edge retired or moved since queued
--   edge-remap: edge exists       requirement already mapped to that anchor
--
-- The new edge copies every content column of the legacy one, including
-- mapping_tier, and records crosswalk_source 'scf-remap:<proposal id>': a
-- crosswalk importer retires only edges whose source is its own (#1971), so
-- re-importing the legacy crosswalk cannot retire an approved remap.
CREATE OR REPLACE FUNCTION scf_edge_remap_decide(
    p_proposal_id UUID,
    p_decision    TEXT,
    p_scf_id      TEXT,
    p_reason      TEXT,
    p_decided_by  TEXT,
    p_bulk        BOOLEAN
)
RETURNS scf_edge_remap_decisions
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
DECLARE
    p        scf_edge_remap_proposals%ROWTYPE;
    e        fw_to_scf_edges%ROWTYPE;
    code     TEXT;
    anchor   UUID;
    new_edge UUID;
    reason   TEXT := NULLIF(btrim(COALESCE(p_reason, '')), '');
    d        scf_edge_remap_decisions%ROWTYPE;
BEGIN
    IF p_decision NOT IN ('approved', 'rejected', 'kept_legacy') THEN
        RAISE EXCEPTION 'edge-remap: unknown decision %', p_decision USING ERRCODE = 'check_violation';
    END IF;
    IF btrim(COALESCE(p_decided_by, '')) = '' THEN
        RAISE EXCEPTION 'edge-remap: decider required' USING ERRCODE = 'check_violation';
    END IF;

    SELECT * INTO p FROM scf_edge_remap_proposals WHERE id = p_proposal_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'edge-remap: not found' USING ERRCODE = 'no_data_found';
    END IF;
    -- open and rejected may still be decided; a reject needs an open one.
    IF p.status NOT IN ('open', 'rejected') OR (p_decision = 'rejected' AND p.status <> 'open') THEN
        RAISE EXCEPTION 'edge-remap: decided (status %)', p.status USING ERRCODE = 'check_violation';
    END IF;

    IF p_decision = 'approved' THEN
        code := NULLIF(btrim(COALESCE(p_scf_id, '')), '');
        IF code IS NULL THEN
            IF p.class IN ('split', 'no_successor') OR cardinality(p.successor_scf_ids) <> 1 THEN
                RAISE EXCEPTION 'edge-remap: anchor required (class %)', p.class USING ERRCODE = 'check_violation';
            END IF;
            code := p.successor_scf_ids[1];
        END IF;

        SELECT * INTO e FROM fw_to_scf_edges WHERE id = p.edge_id FOR UPDATE;
        IF NOT FOUND OR e.retired_at IS NOT NULL OR e.scf_anchor_id <> p.legacy_anchor_id THEN
            RAISE EXCEPTION 'edge-remap: edge changed' USING ERRCODE = 'check_violation';
        END IF;

        SELECT a.id INTO anchor FROM scf_anchors a
        WHERE a.framework_version_id = p.target_framework_version_id AND a.scf_id = code;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'edge-remap: anchor not in release (% in %)', code, p.target_release
                USING ERRCODE = 'check_violation';
        END IF;

        IF EXISTS (SELECT 1 FROM fw_to_scf_edges x
                   WHERE x.framework_requirement_id = e.framework_requirement_id
                     AND x.scf_anchor_id = anchor) THEN
            RAISE EXCEPTION 'edge-remap: edge exists (% already maps to %)', e.framework_requirement_id, code
                USING ERRCODE = 'unique_violation';
        END IF;

        new_edge := gen_random_uuid();
        INSERT INTO fw_to_scf_edges (
            id, framework_requirement_id, scf_anchor_id, relationship_type,
            strength, source_attribution, rationale, mapping_tier, crosswalk_source)
        VALUES (
            new_edge, e.framework_requirement_id, anchor, e.relationship_type,
            e.strength, e.source_attribution, e.rationale, e.mapping_tier,
            'scf-remap:' || p.id::text);

        UPDATE scf_edge_remap_proposals
        SET status = 'approved', target_anchor_id = anchor, new_edge_id = new_edge
        WHERE id = p.id;
    ELSE
        UPDATE scf_edge_remap_proposals SET status = p_decision WHERE id = p.id;
    END IF;

    INSERT INTO scf_edge_remap_decisions (
        proposal_id, decision, bulk, legacy_edge_id, legacy_anchor_id,
        legacy_scf_id, legacy_release, new_edge_id, new_anchor_id,
        new_scf_id, new_release, reason, decided_by)
    VALUES (
        p.id, p_decision, COALESCE(p_bulk, false), p.edge_id, p.legacy_anchor_id,
        p.legacy_scf_id, p.legacy_release, new_edge, anchor,
        CASE WHEN p_decision = 'approved' THEN code END,
        CASE WHEN p_decision = 'approved' THEN p.target_release END,
        reason, btrim(p_decided_by))
    RETURNING * INTO d;
    RETURN d;
END;
$$;

-- scf_edge_remap_lock_open_unchanged locks every open unchanged proposal
-- for the caller's transaction, NOWAIT, and returns their ids. Bulk approval
-- calls it first, so a second bulk run fails fast (55P03) instead of
-- queueing behind the first. The app role holds no UPDATE on the table, so
-- it cannot take a FOR UPDATE lock directly.
CREATE OR REPLACE FUNCTION scf_edge_remap_lock_open_unchanged()
RETURNS SETOF UUID
LANGUAGE sql
SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
    SELECT id FROM scf_edge_remap_proposals
    WHERE class = 'unchanged' AND status = 'open'
    ORDER BY legacy_scf_id, id
    FOR UPDATE NOWAIT
$$;

REVOKE ALL ON FUNCTION scf_edge_remap_lock_open_unchanged() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION scf_edge_remap_lock_open_unchanged() TO atlas_app;

REVOKE ALL ON FUNCTION scf_edge_remap_decide(UUID, TEXT, TEXT, TEXT, TEXT, BOOLEAN) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION scf_edge_remap_decide(UUID, TEXT, TEXT, TEXT, TEXT, BOOLEAN) TO atlas_app;

GRANT SELECT ON scf_edge_remap_proposals TO atlas_app;
GRANT SELECT ON scf_edge_remap_decisions TO atlas_app;
