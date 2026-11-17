-- security-atlas — #2481: the catalog-delete guard locks the parent row
-- before it counts active overlay edges.
--
-- Follow-up from the review of #2474 (#2240). The guard in 20261234000000
-- counts active overlay edges with a plain SELECT. The count is only sound
-- if no overlay INSERT naming this row can commit between the count and the
-- delete. An overlay INSERT's FK check takes FOR KEY SHARE on the
-- requirement and the anchor, which conflicts with FOR UPDATE, so holding
-- FOR UPDATE on OLD before the count closes the window under READ
-- COMMITTED:
--
--   * an insert whose FK check already holds KEY SHARE makes the delete
--     wait until it commits; the count then runs on a fresh snapshot, sees
--     the edge and refuses (23001);
--   * an insert that comes later waits on the delete's lock and fails its
--     FK check (23503) once the row is gone.
--
-- A DELETE already locks its target row before BEFORE ROW triggers run, so
-- on PostgreSQL 16 the lock below is one our own transaction already holds.
-- It is written out so the guard's correctness is a property of this
-- function rather than of executor internals.
--
-- Body otherwise unchanged from 20261234000000; search_path stays pinned.

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
        PERFORM 1 FROM framework_requirements WHERE id = OLD.id FOR UPDATE;
        SELECT count(*) INTO active FROM fw_to_scf_overlay_edges
        WHERE framework_requirement_id = OLD.id AND status = 'active';
    ELSE
        col := 'scf_anchor_id';
        PERFORM 1 FROM scf_anchors WHERE id = OLD.id FOR UPDATE;
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
