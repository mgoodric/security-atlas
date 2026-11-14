-- #1768: freeze imported SCF anchors.
--
-- Once an SCF release is imported its anchors are immutable: the SCF
-- reuses old IDs for different controls between releases, so rewriting
-- an anchor's content in place silently changes what every control and
-- crosswalk edge bound to it means. A changed catalog ships under a new
-- release label (a new framework_versions row with new anchor rows).
--
-- This mirrors trg_framework_requirement_immutability (migration
-- 20260612090000): BEFORE UPDATE, rejected when the anchor's
-- framework_version is current / legacy / withdrawn (every value of
-- framework_version_status on main; a future staged-load value stays
-- editable). atlas_app holds only SELECT on scf_anchors, so for that role
-- the GRANT already refuses the UPDATE; the trigger is what stops the
-- privileged atlas_migrate (importer) role. scfimport refuses a
-- same-label re-import with different content before it writes, so the
-- trigger is the backstop, not the operator-facing error.
--
-- Only content columns are guarded (scf_id, family, title, description,
-- subtopics, framework_version_id). An UPDATE that leaves them unchanged
-- still passes, so a later column added to scf_anchors (legacy SCF IDs,
-- #1769) can be backfilled. DELETE is not guarded, for the reason the
-- framework_requirements trigger gives: dropping a whole obsolete release
-- (CASCADE from framework_versions) is catalog GC, not an in-place edit.

CREATE OR REPLACE FUNCTION enforce_scf_anchor_immutability()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    v_status framework_version_status;
BEGIN
    IF NEW.scf_id IS NOT DISTINCT FROM OLD.scf_id
       AND NEW.family IS NOT DISTINCT FROM OLD.family
       AND NEW.title IS NOT DISTINCT FROM OLD.title
       AND NEW.description IS NOT DISTINCT FROM OLD.description
       AND NEW.subtopics IS NOT DISTINCT FROM OLD.subtopics
       AND NEW.framework_version_id IS NOT DISTINCT FROM OLD.framework_version_id THEN
        RETURN NEW;
    END IF;

    SELECT status INTO v_status
        FROM framework_versions
        WHERE id = OLD.framework_version_id;

    IF v_status IS NOT NULL AND v_status IN ('current', 'legacy', 'withdrawn') THEN
        RAISE EXCEPTION
            'scf_anchors % (%) belongs to an imported SCF release (framework_version % / status=%); imported anchors are frozen — import the changed catalog under a new release label',
            OLD.id, OLD.scf_id, OLD.framework_version_id, v_status
            USING ERRCODE = 'raise_exception';
    END IF;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_scf_anchor_immutability ON scf_anchors;
CREATE TRIGGER trg_scf_anchor_immutability
    BEFORE UPDATE ON scf_anchors
    FOR EACH ROW
    EXECUTE FUNCTION enforce_scf_anchor_immutability();
