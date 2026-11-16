-- security-atlas — #1803: overlay edges carry over to new framework versions
-- (#1735, user story 24).
--
-- When a super_admin approves an exact_code row in the framework-version
-- migration queue (ADR 0019), the requirement has carried over from version
-- N to N+1. Every tenant's ACTIVE overlay edges on the N requirement are
-- copied to the N+1 requirement, each copy in its source edge's own tenant,
-- active immediately (the assertion was already attested), with one
-- fw_to_scf_overlay_edge_audit_log row per copy (action 'carry_over').
--
--   * Edges on N are untouched; retired edges are not copied.
--   * Requirements new in N+1 ('added' rows) get none: only exact_code rows
--     carry anything over.
--   * One-shot: the function stamps the queue row's overlay_carried_over_at
--     and refuses a row already stamped, so a re-run can neither duplicate a
--     copy nor bring back an N+1 copy a tenant has since retired. Within the
--     one run, the active-pair unique index skips a pair the tenant already
--     holds on N+1.
--
-- Why SECURITY DEFINER: the approval is a global catalog act (super_admin,
-- no tenant GUC), but the overlay table is tenant-scoped under FORCE RLS.
-- The function runs as its owner (the BYPASSRLS migration role) and never
-- moves a row across tenants: every copy and its audit row carry the source
-- edge's tenant_id. It does only what the approved migration row implies,
-- and refuses a row that is not an approved exact_code carry-over between
-- two catalog versions of one framework, or a call made under a tenant GUC.

-- When the approved carry-over copied its overlay edges; NULL until then.
ALTER TABLE framework_version_migrations
    ADD COLUMN overlay_carried_over_at TIMESTAMPTZ NULL;

ALTER TABLE fw_to_scf_overlay_edge_audit_log
    DROP CONSTRAINT fw_to_scf_overlay_edge_audit_log_action_check;
ALTER TABLE fw_to_scf_overlay_edge_audit_log
    ADD CONSTRAINT fw_to_scf_overlay_edge_audit_log_action_check
    CHECK (action IN ('create', 'retire', 'carry_over'));

CREATE OR REPLACE FUNCTION carry_over_overlay_edges(p_migration_id UUID, p_actor TEXT)
RETURNS INTEGER
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
DECLARE
    m        framework_version_migrations%ROWTYPE;
    inserted INTEGER;
    audited  INTEGER;
BEGIN
    -- A catalog act: a caller in a tenant context has no business here.
    IF coalesce(current_setting('app.current_tenant', true), '') <> '' THEN
        RAISE EXCEPTION 'carry_over_overlay_edges: refused under a tenant context'
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF p_actor IS NULL OR length(p_actor) = 0 THEN
        RAISE EXCEPTION 'carry_over_overlay_edges: actor required'
            USING ERRCODE = 'check_violation';
    END IF;

    SELECT * INTO m FROM framework_version_migrations WHERE id = p_migration_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'carry_over_overlay_edges: migration % not found', p_migration_id
            USING ERRCODE = 'no_data_found';
    END IF;
    IF m.status <> 'approved' OR m.match_kind <> 'exact_code'
        OR m.from_requirement_id IS NULL OR m.to_requirement_id IS NULL THEN
        RAISE EXCEPTION 'carry_over_overlay_edges: migration % is not an approved carry-over', p_migration_id
            USING ERRCODE = 'check_violation';
    END IF;
    IF m.overlay_carried_over_at IS NOT NULL THEN
        RAISE EXCEPTION 'carry_over_overlay_edges: migration % already carried over at %',
            p_migration_id, m.overlay_carried_over_at
            USING ERRCODE = 'check_violation';
    END IF;
    -- Each requirement belongs to its side's version, and both versions are
    -- catalog versions of the migration row's framework.
    PERFORM 1
    FROM framework_requirements fr_from
    JOIN framework_versions v_from ON v_from.id = fr_from.framework_version_id
    JOIN framework_requirements fr_to ON fr_to.id = m.to_requirement_id
    JOIN framework_versions v_to ON v_to.id = fr_to.framework_version_id
    WHERE fr_from.id = m.from_requirement_id
      AND v_from.id = m.from_version_id
      AND v_to.id = m.to_version_id
      AND v_from.framework_id = m.framework_id
      AND v_to.framework_id = m.framework_id
      AND v_from.tenant_id IS NULL
      AND v_to.tenant_id IS NULL;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'carry_over_overlay_edges: migration % requirements do not match its catalog versions', p_migration_id
            USING ERRCODE = 'check_violation';
    END IF;

    UPDATE framework_version_migrations
    SET overlay_carried_over_at = now()
    WHERE id = p_migration_id;

    WITH src AS (
        SELECT e.*
        FROM fw_to_scf_overlay_edges e
        WHERE e.framework_requirement_id = m.from_requirement_id
          AND e.status = 'active'
    ),
    ins AS (
        INSERT INTO fw_to_scf_overlay_edges (
            tenant_id, framework_requirement_id, scf_anchor_id,
            relationship_type, strength, rationale, source_ref, created_by
        )
        SELECT s.tenant_id, m.to_requirement_id, s.scf_anchor_id,
               s.relationship_type, s.strength, s.rationale, s.source_ref, s.created_by
        FROM src s
        ON CONFLICT (tenant_id, framework_requirement_id, scf_anchor_id)
            WHERE status = 'active'
            DO NOTHING
        RETURNING *
    ),
    aud AS (
        INSERT INTO fw_to_scf_overlay_edge_audit_log (
            tenant_id, overlay_edge_id, action, before, after, actor
        )
        SELECT i.tenant_id, i.id, 'carry_over', NULL,
               jsonb_build_object(
                   'framework', f.slug,
                   'framework_version', fv.version,
                   'requirement_code', fr.code,
                   'scf_release', av.version,
                   'anchor_code', a.scf_id,
                   'relationship_type', i.relationship_type,
                   'strength', i.strength,
                   'rationale', i.rationale,
                   'source_ref', i.source_ref,
                   'status', i.status,
                   'carried_from_edge_id', s.id,
                   'migration_id', m.id
               ),
               p_actor
        FROM ins i
        JOIN src s ON s.tenant_id = i.tenant_id AND s.scf_anchor_id = i.scf_anchor_id
        JOIN framework_requirements fr ON fr.id = i.framework_requirement_id
        JOIN framework_versions fv ON fv.id = fr.framework_version_id
        JOIN frameworks f ON f.id = fv.framework_id
        JOIN scf_anchors a ON a.id = i.scf_anchor_id
        JOIN framework_versions av ON av.id = a.framework_version_id
        RETURNING 1
    )
    SELECT (SELECT count(*) FROM ins), (SELECT count(*) FROM aud)
    INTO inserted, audited;

    -- Every copy has its audit row, or nothing is copied.
    IF inserted <> audited THEN
        RAISE EXCEPTION 'carry_over_overlay_edges: % edges copied but % audited', inserted, audited
            USING ERRCODE = 'internal_error';
    END IF;

    RETURN inserted;
END;
$$;

REVOKE ALL ON FUNCTION carry_over_overlay_edges(UUID, TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION carry_over_overlay_edges(UUID, TEXT) TO atlas_app;
