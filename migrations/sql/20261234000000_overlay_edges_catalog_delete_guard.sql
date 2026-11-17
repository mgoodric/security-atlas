-- security-atlas — #2240: overlay edges hold their catalog rows, record any
-- cascade, and may only name a requirement their tenant can see.
--
-- Follow-ups from the review of #2220 (#1797). Three triggers:
--
-- 1. Refuse a catalog delete that would remove an ACTIVE overlay edge.
--    fw_to_scf_overlay_edges' FKs to framework_requirements and scf_anchors
--    cascade (#1797 D4), so deleting a requirement or an anchor (directly,
--    or through its framework version) removed tenants' live overlay edges
--    with nothing on record. A BEFORE DELETE row trigger on each catalog
--    table now raises restrict_violation (23001) while an active overlay
--    edge references the row. Retire the overlay edges first.
--
-- 2. Record the cascade that is still allowed. A retired overlay edge is
--    history, not a live assertion, so it does not hold the catalog row;
--    the FK still cascades it away. The same trigger writes one
--    fw_to_scf_overlay_edge_audit_log row per such edge, action
--    'cascade_removed', in the deleting transaction, before the cascade
--    runs (the requirement and anchor codes are still readable then; a
--    row removed through a framework version delete can lose its version
--    label, so the codes are LEFT JOINed).
--
--    The FK stays CASCADE rather than RESTRICT for the retired case: a
--    RESTRICT would make every catalog-wipe path (fullReseedSCF, test
--    fixtures) delete tenant history first, and the audit trail already
--    outlives the edge (it has no FK to it).
--
-- 3. An overlay edge's requirement belongs to a catalog framework version
--    (tenant_id IS NULL) or to the overlay's own tenant. The FK to
--    framework_requirements did not check this; only the API resolver kept
--    it to catalog rows. BEFORE INSERT OR UPDATE on the overlay table.
--
-- All three functions are SECURITY DEFINER: they must see every tenant's
-- overlay edges and framework versions regardless of the caller's RLS
-- context (a catalog delete runs with no tenant GUC; atlas_app cannot see
-- another tenant's private framework version, and "not visible" must read
-- as refused, not as absent). They read, and the delete trigger writes
-- audit rows carrying each edge's own tenant_id; none moves data across
-- tenants.
--
-- TRUNCATE bypasses row triggers. TRUNCATE ... CASCADE on a catalog table
-- also truncates the overlay table, so it cannot leave a dangling edge, but
-- it records nothing. Only test fixtures truncate catalog tables.
--
-- Existing rows: the tenant check fires on new writes only. Run
-- scripts/dry-run-2240-overlay-requirement-tenant.sql to list any existing
-- overlay edge whose requirement belongs to another tenant's version.

ALTER TABLE fw_to_scf_overlay_edge_audit_log
    DROP CONSTRAINT fw_to_scf_overlay_edge_audit_log_action_check;
ALTER TABLE fw_to_scf_overlay_edge_audit_log
    ADD CONSTRAINT fw_to_scf_overlay_edge_audit_log_action_check
    CHECK (action IN ('create', 'retire', 'carry_over', 'cascade_removed'));

-- ===== 1 + 2: catalog deletes =====

CREATE OR REPLACE FUNCTION fw_to_scf_overlay_edges_guard_catalog_delete()
RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
DECLARE
    col    TEXT;
    active INTEGER;
BEGIN
    IF TG_TABLE_NAME = 'framework_requirements' THEN
        col := 'framework_requirement_id';
        SELECT count(*) INTO active FROM fw_to_scf_overlay_edges
        WHERE framework_requirement_id = OLD.id AND status = 'active';
    ELSE
        col := 'scf_anchor_id';
        SELECT count(*) INTO active FROM fw_to_scf_overlay_edges
        WHERE scf_anchor_id = OLD.id AND status = 'active';
    END IF;

    IF active > 0 THEN
        RAISE EXCEPTION '%: % active overlay edge(s) reference % %; retire them first',
            TG_TABLE_NAME, active, TG_TABLE_NAME, OLD.id
            USING ERRCODE = 'restrict_violation',
                  HINT = 'POST /v1/mapping-overlays/{id}/retire, then delete the catalog row.';
    END IF;

    INSERT INTO fw_to_scf_overlay_edge_audit_log (
        tenant_id, overlay_edge_id, action, before, after, actor
    )
    SELECT e.tenant_id, e.id, 'cascade_removed',
           jsonb_build_object(
               'framework', f.slug,
               'framework_version', fv.version,
               'requirement_code', fr.code,
               'scf_release', av.version,
               'anchor_code', a.scf_id,
               'relationship_type', e.relationship_type,
               'strength', e.strength,
               'rationale', e.rationale,
               'source_ref', e.source_ref,
               'status', e.status,
               'retirement_reason', e.retirement_reason
           ),
           jsonb_build_object(
               'status', 'removed',
               'removed_with', TG_TABLE_NAME,
               'removed_row_id', OLD.id,
               'framework_requirement_id', e.framework_requirement_id,
               'scf_anchor_id', e.scf_anchor_id
           ),
           'system:catalog_delete'
    FROM fw_to_scf_overlay_edges e
    LEFT JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
    LEFT JOIN framework_versions fv ON fv.id = fr.framework_version_id
    LEFT JOIN frameworks f ON f.id = fv.framework_id
    LEFT JOIN scf_anchors a ON a.id = e.scf_anchor_id
    LEFT JOIN framework_versions av ON av.id = a.framework_version_id
    WHERE (col = 'framework_requirement_id' AND e.framework_requirement_id = OLD.id)
       OR (col = 'scf_anchor_id' AND e.scf_anchor_id = OLD.id);

    RETURN OLD;
END;
$$;

REVOKE ALL ON FUNCTION fw_to_scf_overlay_edges_guard_catalog_delete() FROM PUBLIC;

CREATE TRIGGER fw_to_scf_overlay_edges_guard_requirement_delete_trg
    BEFORE DELETE ON framework_requirements
    FOR EACH ROW
    EXECUTE FUNCTION fw_to_scf_overlay_edges_guard_catalog_delete();

CREATE TRIGGER fw_to_scf_overlay_edges_guard_anchor_delete_trg
    BEFORE DELETE ON scf_anchors
    FOR EACH ROW
    EXECUTE FUNCTION fw_to_scf_overlay_edges_guard_catalog_delete();

-- ===== 3: the requirement's tenant =====

CREATE OR REPLACE FUNCTION fw_to_scf_overlay_edges_guard_requirement_tenant()
RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
DECLARE
    owner_tenant UUID;
BEGIN
    SELECT fv.tenant_id INTO owner_tenant
    FROM framework_requirements fr
    JOIN framework_versions fv ON fv.id = fr.framework_version_id
    WHERE fr.id = NEW.framework_requirement_id;
    -- A missing requirement is the FK's error to raise, not this trigger's.
    IF NOT FOUND THEN
        RETURN NEW;
    END IF;
    IF owner_tenant IS NOT NULL AND owner_tenant IS DISTINCT FROM NEW.tenant_id THEN
        RAISE EXCEPTION 'fw_to_scf_overlay_edges: requirement % belongs to another tenant''s framework version',
            NEW.framework_requirement_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

REVOKE ALL ON FUNCTION fw_to_scf_overlay_edges_guard_requirement_tenant() FROM PUBLIC;

CREATE TRIGGER fw_to_scf_overlay_edges_guard_requirement_tenant_trg
    BEFORE INSERT OR UPDATE OF tenant_id, framework_requirement_id ON fw_to_scf_overlay_edges
    FOR EACH ROW
    EXECUTE FUNCTION fw_to_scf_overlay_edges_guard_requirement_tenant();
