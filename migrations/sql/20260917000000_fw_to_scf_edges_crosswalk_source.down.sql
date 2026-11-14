-- Reverse #1971. Restore the effective-edges view to its migration
-- 20260821000000 shape first (it references retired_at), then drop the two
-- columns. Retired edges become live again, since nothing marks them any
-- more; the tier-transition rows that recorded each retirement stay.

CREATE OR REPLACE VIEW effective_fw_to_scf_edges
WITH (security_invoker = true) AS
SELECT
    e.id,
    e.framework_requirement_id,
    e.scf_anchor_id,
    e.relationship_type,
    e.strength,
    e.source_attribution,
    e.rationale,
    e.created_at,
    e.updated_at,
    e.mapping_tier
FROM fw_to_scf_edges e;

ALTER TABLE fw_to_scf_edges
    DROP COLUMN IF EXISTS retired_at,
    DROP COLUMN IF EXISTS crosswalk_source;
