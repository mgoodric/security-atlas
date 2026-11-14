-- security-atlas — #1797: tenant overlay edges (create, list, retire), the
-- first step of #1735.
--
-- An overlay edge is one organisation's own requirement -> SCF anchor edge:
-- "our auditor tested this control under CC1.1". It is the tenant-scoped
-- override table that 20260511000013's header promised. fw_to_scf_edges
-- stays global catalog data and read-only to atlas_app; nothing here grants
-- on it.
--
-- Shape:
--   * relationship_type and strength use the catalog-edge vocabularies
--     (strm_relationship_type, 0.0..1.0).
--   * rationale and source_ref are both required: every org mapping is
--     traceable to the evidence behind it.
--   * status is active | retired. Retiring keeps the row (reason, actor,
--     time); a retired edge never becomes active again — add a new one.
--   * No mapping_tier: an overlay edge is the organisation's own attested
--     assertion, so ADR 0018's tiers do not apply.
--   * Unique per (tenant, requirement, anchor) among active rows only, so a
--     retired edge's history does not block re-adding the pair.
--
-- The FKs cascade like fw_to_scf_edges': an edge without its requirement or
-- anchor is meaningless. Carrying edges across a framework version or an SCF
-- release is #1735's later steps (migration queue, remap queue).
--
-- Coverage does not read this table yet; adding it to
-- effective_fw_to_scf_edges is the coverage-union step of #1735.

CREATE TABLE fw_to_scf_overlay_edges (
    id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id                 UUID NOT NULL,
    framework_requirement_id  UUID NOT NULL REFERENCES framework_requirements(id) ON DELETE CASCADE,
    scf_anchor_id             UUID NOT NULL REFERENCES scf_anchors(id) ON DELETE CASCADE,
    relationship_type         strm_relationship_type NOT NULL,
    strength                  DOUBLE PRECISION NOT NULL,
    rationale                 TEXT NOT NULL,
    source_ref                TEXT NOT NULL,
    status                    TEXT NOT NULL DEFAULT 'active',
    retirement_reason         TEXT NULL,
    created_by                TEXT NOT NULL,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    retired_by                TEXT NULL,
    retired_at                TIMESTAMPTZ NULL,
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fw_to_scf_overlay_edges_strength_range
        CHECK (strength >= 0.0 AND strength <= 1.0),
    CONSTRAINT fw_to_scf_overlay_edges_rationale_present
        CHECK (length(btrim(rationale)) > 0 AND length(rationale) <= 4000),
    CONSTRAINT fw_to_scf_overlay_edges_source_ref_present
        CHECK (length(btrim(source_ref)) > 0 AND length(source_ref) <= 1000),
    CONSTRAINT fw_to_scf_overlay_edges_status_check
        CHECK (status IN ('active', 'retired')),
    CONSTRAINT fw_to_scf_overlay_edges_created_by_nonempty
        CHECK (length(created_by) > 0),
    -- Retirement fields are all set exactly when the edge is retired.
    CONSTRAINT fw_to_scf_overlay_edges_retirement_shape
        CHECK (
            (status = 'active'
                AND retirement_reason IS NULL AND retired_by IS NULL AND retired_at IS NULL)
            OR
            (status = 'retired'
                AND retirement_reason IS NOT NULL AND length(btrim(retirement_reason)) > 0
                AND length(retirement_reason) <= 4000
                AND retired_by IS NOT NULL AND length(retired_by) > 0
                AND retired_at IS NOT NULL)
        )
);

CREATE UNIQUE INDEX fw_to_scf_overlay_edges_active_pair_unique
    ON fw_to_scf_overlay_edges (tenant_id, framework_requirement_id, scf_anchor_id)
    WHERE status = 'active';

CREATE INDEX idx_fw_to_scf_overlay_edges_tenant_status
    ON fw_to_scf_overlay_edges (tenant_id, status, created_at DESC);
CREATE INDEX idx_fw_to_scf_overlay_edges_requirement
    ON fw_to_scf_overlay_edges (framework_requirement_id);
CREATE INDEX idx_fw_to_scf_overlay_edges_anchor
    ON fw_to_scf_overlay_edges (scf_anchor_id);

-- An edge's identity and assertion never change after creation; the only
-- update is active -> retired. Correcting an edge is retire + add.
CREATE OR REPLACE FUNCTION fw_to_scf_overlay_edges_guard_update()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.framework_requirement_id IS DISTINCT FROM OLD.framework_requirement_id
        OR NEW.scf_anchor_id IS DISTINCT FROM OLD.scf_anchor_id
        OR NEW.relationship_type IS DISTINCT FROM OLD.relationship_type
        OR NEW.strength IS DISTINCT FROM OLD.strength
        OR NEW.rationale IS DISTINCT FROM OLD.rationale
        OR NEW.source_ref IS DISTINCT FROM OLD.source_ref
        OR NEW.created_by IS DISTINCT FROM OLD.created_by
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'fw_to_scf_overlay_edges: only retirement may change an overlay edge'
            USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.status = 'retired' THEN
        RAISE EXCEPTION 'fw_to_scf_overlay_edges: a retired overlay edge cannot change'
            USING ERRCODE = 'check_violation';
    END IF;
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER fw_to_scf_overlay_edges_guard_update_trg
    BEFORE UPDATE ON fw_to_scf_overlay_edges
    FOR EACH ROW
    EXECUTE FUNCTION fw_to_scf_overlay_edges_guard_update();

ALTER TABLE fw_to_scf_overlay_edges ENABLE ROW LEVEL SECURITY;
ALTER TABLE fw_to_scf_overlay_edges FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON fw_to_scf_overlay_edges
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON fw_to_scf_overlay_edges
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON fw_to_scf_overlay_edges
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON fw_to_scf_overlay_edges
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON fw_to_scf_overlay_edges TO atlas_app;

-- ===== overlay edge audit trail =====
--
-- One row per create and retire, written in the same transaction as the
-- change. Shape follows taxonomy_audit_log (#1810): SELECT + INSERT policies
-- only, plus a trigger that denies UPDATE and DELETE even for a role that
-- bypasses RLS. Rows appear in the unified admin audit log as kind
-- `mapping_overlay`. No FK to the edge: the trail outlives a cascaded
-- delete of its requirement or anchor.

CREATE TABLE fw_to_scf_overlay_edge_audit_log (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL,
    overlay_edge_id UUID NOT NULL,
    action          TEXT NOT NULL
                    CONSTRAINT fw_to_scf_overlay_edge_audit_log_action_check
                    CHECK (action IN ('create', 'retire')),
    -- before is NULL on create.
    before          JSONB NULL,
    after           JSONB NOT NULL,
    actor           TEXT NOT NULL,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Slice 180 convention for the audit-log family.
    subject_module  TEXT NOT NULL DEFAULT 'core',

    CONSTRAINT fw_to_scf_overlay_edge_audit_log_actor_nonempty CHECK (length(actor) > 0)
);

CREATE INDEX idx_fw_to_scf_overlay_edge_audit_log_tenant_occurred
    ON fw_to_scf_overlay_edge_audit_log (tenant_id, occurred_at DESC);
CREATE INDEX idx_fw_to_scf_overlay_edge_audit_log_edge
    ON fw_to_scf_overlay_edge_audit_log (tenant_id, overlay_edge_id, occurred_at);

ALTER TABLE fw_to_scf_overlay_edge_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE fw_to_scf_overlay_edge_audit_log FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON fw_to_scf_overlay_edge_audit_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON fw_to_scf_overlay_edge_audit_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT ON fw_to_scf_overlay_edge_audit_log TO atlas_app;

CREATE OR REPLACE FUNCTION fw_to_scf_overlay_edge_audit_log_append_only()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'fw_to_scf_overlay_edge_audit_log is append-only: % denied', TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER fw_to_scf_overlay_edge_audit_log_append_only_trg
    BEFORE UPDATE OR DELETE ON fw_to_scf_overlay_edge_audit_log
    FOR EACH ROW
    EXECUTE FUNCTION fw_to_scf_overlay_edge_audit_log_append_only();
