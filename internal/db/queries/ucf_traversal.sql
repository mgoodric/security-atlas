-- Slice 008: UCF graph traversal queries.
--
-- All traversals go through the SCF anchor spine (constitutional invariant 1
-- per CLAUDE.md / canvas §3.1). The path is two index-backed JOINs, not a
-- recursive CTE — see Plans/UCF_GRAPH_MODEL.md §7. Fan-out is bounded
-- (typical requirement maps to 1-6 anchors; typical anchor maps to 1-8
-- requirements) so the queries return in milliseconds at production
-- cardinality.
--
-- `no_relationship` STRM edges are filtered out of coverage responses
-- (canvas §3.2 stores them as data to suppress false suggestions; they
-- shouldn't appear in coverage views).
--
-- Tenant scoping: queries that touch the `controls` table run inside the
-- request's `app.current_tenant` GUC set by tenancy.Middleware; RLS does
-- the filtering. Catalog tables (`framework_requirements`,
-- `fw_to_scf_edges`, `scf_anchors`, `framework_versions`) have no
-- tenant_id and no RLS — global, platform-bundled. No app-level
-- `WHERE tenant_id = ?` clause is permitted on the controls reads
-- (constitutional invariant 6 per CLAUDE.md / canvas §5.4).

-- name: ListAnchorsForRequirementWithEdges :many
-- AC-1 (anchors arm). Given a framework_requirement id, return every
-- non-`no_relationship` STRM edge to an SCF anchor, joined to scf_anchors
-- so callers get scf_id + family + title in one round-trip. Sorted by
-- strength DESC (strongest matches first) then scf_id (stable order).
SELECT
    e.id                AS edge_id,
    e.scf_anchor_id,
    e.relationship_type,
    e.strength,
    e.source_attribution,
    e.mapping_tier,
    e.rationale,
    a.scf_id,
    a.family,
    a.title            AS anchor_title,
    a.description      AS anchor_description
FROM fw_to_scf_edges e
JOIN scf_anchors a ON a.id = e.scf_anchor_id
WHERE e.framework_requirement_id = $1
  AND e.relationship_type <> 'no_relationship'
ORDER BY e.strength DESC, a.scf_id;

-- name: ListAnchorsForRequirementWithEdgesByFrameworkVersion :many
-- AC-1 + AC-4 (historical pinning arm). Same as
-- ListAnchorsForRequirementWithEdges, but additionally filtered by the
-- SCF framework_version_id so callers can pin to a specific SCF
-- release. When no SCF release is pinned, callers use the unfiltered
-- variant above.
SELECT
    e.id                AS edge_id,
    e.scf_anchor_id,
    e.relationship_type,
    e.strength,
    e.source_attribution,
    e.mapping_tier,
    e.rationale,
    a.scf_id,
    a.family,
    a.title            AS anchor_title,
    a.description      AS anchor_description
FROM fw_to_scf_edges e
JOIN scf_anchors a ON a.id = e.scf_anchor_id
WHERE e.framework_requirement_id = $1
  AND e.relationship_type <> 'no_relationship'
  AND a.framework_version_id = $2
ORDER BY e.strength DESC, a.scf_id;

-- name: ListControlsForAnchors :many
-- AC-1 (controls arm). Given a list of SCF anchor ids, return every
-- active control anchored on any of them. Runs inside the tenant GUC so
-- RLS filters to the caller's tenant. Supersededs are excluded —
-- coverage only counts the currently active control versions.
--
-- NO `WHERE tenant_id = ?` clause: invariant 6 — RLS does the tenant
-- filtering. Adding such a clause would be a constitutional violation.
SELECT
    c.id,
    c.bundle_id,
    c.version,
    c.scf_id,
    c.scf_anchor_id,
    c.title,
    c.description,
    c.control_family,
    c.implementation_type,
    c.owner_role,
    c.lifecycle_state,
    c.applicability_expr,
    c.freshness_class,
    c.created_at
FROM controls c
WHERE c.scf_anchor_id = ANY($1::uuid[])
  AND c.superseded_by IS NULL
ORDER BY c.bundle_id;

-- name: ListRequirementsForAnchor :many
-- AC-2. Reverse traversal — given an SCF anchor, return every framework
-- requirement it satisfies, joined to framework_versions + frameworks so
-- callers see the full natural key (slug + version + code) in one
-- round-trip. Sorted by framework slug + version + code for stable
-- output. `no_relationship` edges excluded.
SELECT
    e.id               AS edge_id,
    e.framework_requirement_id,
    e.relationship_type,
    e.strength,
    e.source_attribution,
    e.mapping_tier,
    e.rationale,
    r.code,
    r.title            AS requirement_title,
    r.body             AS requirement_body,
    fv.id              AS framework_version_id,
    fv.version         AS framework_version,
    fv.status          AS framework_version_status,
    f.slug             AS framework_slug,
    f.name             AS framework_name
FROM fw_to_scf_edges e
JOIN framework_requirements r ON r.id = e.framework_requirement_id
JOIN framework_versions fv    ON fv.id = r.framework_version_id
JOIN frameworks f             ON f.id = fv.framework_id
WHERE e.scf_anchor_id = $1
  AND e.relationship_type <> 'no_relationship'
ORDER BY f.slug, fv.version, r.code;

-- name: ListRequirementsForAnchorByFrameworkVersion :many
-- AC-2 + AC-4 (framework_version pinning). Same as
-- ListRequirementsForAnchor but filtered to a specific framework
-- version id. Used when the caller passes ?framework_version=slug:version.
SELECT
    e.id               AS edge_id,
    e.framework_requirement_id,
    e.relationship_type,
    e.strength,
    e.source_attribution,
    e.mapping_tier,
    e.rationale,
    r.code,
    r.title            AS requirement_title,
    r.body             AS requirement_body,
    fv.id              AS framework_version_id,
    fv.version         AS framework_version,
    fv.status          AS framework_version_status,
    f.slug             AS framework_slug,
    f.name             AS framework_name
FROM fw_to_scf_edges e
JOIN framework_requirements r ON r.id = e.framework_requirement_id
JOIN framework_versions fv    ON fv.id = r.framework_version_id
JOIN frameworks f             ON f.id = fv.framework_id
WHERE e.scf_anchor_id = $1
  AND e.relationship_type <> 'no_relationship'
  AND r.framework_version_id = $2
ORDER BY f.slug, fv.version, r.code;

-- name: GetFrameworkVersionBySlugAndVersion :one
-- Resolves "?framework_version=slug:version" into a framework_versions
-- row. Used by both the anchor->requirements and control->coverage
-- handlers to translate the URL param into a stable id for the pinned
-- traversal. NULL tenant_id constraint scopes to the global catalog.
SELECT fv.*
FROM framework_versions fv
JOIN frameworks f ON f.id = fv.framework_id
WHERE f.slug = $1
  AND fv.version = $2
  AND f.tenant_id IS NULL;
