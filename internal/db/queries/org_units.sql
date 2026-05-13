-- name: CreateOrgUnit :one
-- Insert a new org_unit. Application enforces parent.level >= child.level
-- (team rolls up to org rolls up to company); the DB lets any valid level
-- combination through so partial-load / migration paths still work.
INSERT INTO org_units (
    id, tenant_id, name, parent_id, level, acceptance_authorities
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetOrgUnitByID :one
SELECT *
FROM org_units
WHERE tenant_id = $1 AND id = $2;

-- name: ListOrgUnits :many
SELECT *
FROM org_units
WHERE tenant_id = $1
ORDER BY level, name, id;

-- name: ListOrgUnitChildren :many
-- Direct children only (single hop). Recursive descent is the caller's
-- responsibility; for tree walks the application uses a recursive CTE
-- generated in code (no static sqlc query yet — see slice 056).
SELECT *
FROM org_units
WHERE tenant_id = $1 AND parent_id = $2
ORDER BY name, id;

-- name: UpdateOrgUnit :one
UPDATE org_units
SET name = $3,
    parent_id = $4,
    level = $5,
    acceptance_authorities = $6,
    updated_at = now()
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: DeleteOrgUnit :exec
-- ON DELETE SET NULL on risks.org_unit_id keeps risks alive after their
-- binding org_unit is removed (canvas §6.4: child risk lifecycle is
-- independent of parent).
DELETE FROM org_units
WHERE tenant_id = $1 AND id = $2;

-- name: ParentChainIDs :many
-- Slice 053 cycle detection (AC-4). Returns every node id in the parent_id
-- chain starting at $2 ("the proposed parent"), tenant-scoped to $1. The
-- application checks whether $3 ("the node whose parent we're setting")
-- appears in the returned list — if it does, setting parent_id = $2 would
-- create a cycle.
--
-- Self-parent (parent_id = self_id) is the trivial cycle and is also caught
-- in the application: when $2 == $3, the seed row of the CTE matches and
-- $3 appears in the returned list.
--
-- The CTE is bounded by the tenant_id predicate on every row, so it can
-- never walk into another tenant's chain even with RLS off.
WITH RECURSIVE chain AS (
    SELECT o.id AS node_id, o.parent_id AS up_id
    FROM org_units o
    WHERE o.tenant_id = $1 AND o.id = $2
    UNION ALL
    SELECT u.id AS node_id, u.parent_id AS up_id
    FROM org_units u
    JOIN chain ON u.id = chain.up_id
    WHERE u.tenant_id = $1
)
SELECT node_id FROM chain;
