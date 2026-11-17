-- Reverses 20261237000000_overlay_edges_catalog_delete_lock.sql: restores the
-- 20261234000000 guard body, which counts without the explicit row lock.

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
