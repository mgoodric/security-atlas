-- #1801: the "audited under" report (#1735). Every query runs under the
-- caller's tenant (RLS on controls and, through effective_fw_to_scf_edges,
-- on fw_to_scf_overlay_edges); the controls read also passes tenant_id
-- explicitly as a second leg, the house style of the overlay queries
-- (fw_to_scf_overlay_edges.sql).

-- name: ListAuditedUnderGaps :many
-- One row per (active control, tested-under criterion) pair the control's
-- anchor does not reach. A tested-under reference is an external_refs
-- element with kind 'tested_under': system is the framework slug, id the
-- requirement code, resolved in the framework's CURRENT version of the
-- global catalog. A reference that resolves to no requirement there is not
-- a row (it names no criterion the report could close).
--
-- "Reaches" is coverage's rule: a live edge in effective_fw_to_scf_edges
-- (catalog edges plus the tenant's active overlay edges) from the criterion
-- to the control's own anchor, not no_relationship, not rejected. A control
-- with no anchor reaches nothing, so every criterion it was tested under is
-- a row. Superseded control versions are left out, as in coverage.
SELECT
    c.id               AS control_id,
    c.bundle_id        AS control_bundle_id,
    c.title            AS control_title,
    c.anchor_fit,
    c.anchor_fit_note,
    c.scf_anchor_id,
    a.scf_id           AS anchor_code,
    av.version         AS anchor_scf_release,
    f.slug             AS framework_slug,
    fv.version         AS framework_version,
    r.id               AS requirement_id,
    r.code             AS requirement_code,
    r.title            AS requirement_title,
    coalesce(ref->>'note', '')::text AS tested_under_note
FROM controls c
CROSS JOIN LATERAL jsonb_array_elements(c.external_refs) ref
JOIN frameworks f             ON f.slug = ref->>'system' AND f.tenant_id IS NULL
JOIN framework_versions fv    ON fv.framework_id = f.id AND fv.tenant_id IS NULL AND fv.status = 'current'
JOIN framework_requirements r ON r.framework_version_id = fv.id AND r.code = ref->>'id'
LEFT JOIN scf_anchors a         ON a.id = c.scf_anchor_id
LEFT JOIN framework_versions av ON av.id = a.framework_version_id
WHERE c.tenant_id = sqlc.arg(tenant_id)
  AND c.superseded_by IS NULL
  AND ref->>'kind' = 'tested_under'
  AND (sqlc.narg(framework_slug)::text IS NULL OR f.slug = sqlc.narg(framework_slug)::text)
  AND NOT EXISTS (
      SELECT 1
      FROM effective_fw_to_scf_edges e
      WHERE e.framework_requirement_id = r.id
        AND e.scf_anchor_id = c.scf_anchor_id
        AND e.relationship_type <> 'no_relationship'
        AND e.mapping_tier <> 'rejected'
  )
ORDER BY f.slug, r.code, c.bundle_id, c.id;

-- name: ListCatalogAnchorsForRequirements :many
-- The report's candidate anchors: for each requirement, the SCF anchors a
-- live CATALOG edge joins it to (overlay edges are the organisation's own
-- assertion, not a re-anchoring suggestion). Pinned to one SCF release when
-- scf_release_id is set (the current release, as coverage defaults to).
SELECT
    e.framework_requirement_id,
    a.id               AS scf_anchor_id,
    a.scf_id           AS anchor_code,
    a.title            AS anchor_title,
    e.relationship_type,
    e.strength
FROM effective_fw_to_scf_edges e
JOIN scf_anchors a ON a.id = e.scf_anchor_id
WHERE e.framework_requirement_id = ANY(sqlc.arg(requirement_ids)::uuid[])
  AND e.origin = 'catalog'
  AND e.relationship_type <> 'no_relationship'
  AND e.mapping_tier <> 'rejected'
  AND (sqlc.narg(scf_release_id)::uuid IS NULL OR a.framework_version_id = sqlc.narg(scf_release_id)::uuid)
ORDER BY e.framework_requirement_id, e.strength DESC, a.scf_id;
