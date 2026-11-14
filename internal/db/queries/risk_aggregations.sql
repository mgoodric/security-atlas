-- name: LinkRiskAggregation :exec
-- Link a parent risk to a child risk. rule_id is NULL for manual linkage;
-- slice 054 sets it when an aggregation rule creates the link automatically.
-- Idempotent — ON CONFLICT DO NOTHING so re-linking is a no-op.
INSERT INTO risk_aggregations (parent_risk_id, child_risk_id, rule_id, tenant_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (parent_risk_id, child_risk_id) DO NOTHING;

-- name: UnlinkRiskAggregation :exec
DELETE FROM risk_aggregations
WHERE tenant_id = $1 AND parent_risk_id = $2 AND child_risk_id = $3;

-- name: ListRiskAggregationChildren :many
-- All child risks rolled up under this parent.
SELECT child_risk_id, rule_id, created_at
FROM risk_aggregations
WHERE tenant_id = $1 AND parent_risk_id = $2
ORDER BY created_at ASC, child_risk_id ASC;

-- name: ListRiskAggregationParents :many
-- All parent risks this child rolls up to. Children can roll up to multiple
-- parents (e.g., an ownership-themed risk feeds both an org-level ownership
-- meta-risk and a team-level meta-risk).
SELECT parent_risk_id, rule_id, created_at
FROM risk_aggregations
WHERE tenant_id = $1 AND child_risk_id = $2
ORDER BY created_at ASC, parent_risk_id ASC;

-- name: RiskAggregationReaches :one
-- Reports whether target_risk_id is from_risk_id or one of its descendants
-- (children, grandchildren, ...). Linking parent P -> child C would close a
-- cycle exactly when C reaches P (#1748). UNION, not UNION ALL, so the walk
-- terminates even over a graph that already holds a cycle.
WITH RECURSIVE descendants(id) AS (
    SELECT ra.child_risk_id
    FROM risk_aggregations ra
    WHERE ra.tenant_id = sqlc.arg(tenant_id) AND ra.parent_risk_id = sqlc.arg(from_risk_id)
    UNION
    SELECT ra.child_risk_id
    FROM risk_aggregations ra
    JOIN descendants d ON ra.parent_risk_id = d.id
    WHERE ra.tenant_id = sqlc.arg(tenant_id)
)
SELECT (sqlc.arg(target_risk_id)::uuid = sqlc.arg(from_risk_id)::uuid
        OR EXISTS (SELECT 1 FROM descendants WHERE id = sqlc.arg(target_risk_id)::uuid))::boolean AS reaches;

-- name: ListRiskChildLinks :many
-- The children of a parent risk with the fields the hierarchy views show
-- (#1748). rule_id NULL means the link was made by hand.
SELECT r.id, r.title, r.level, r.org_unit_id, r.methodology, r.inherent_score,
       ra.rule_id, ra.created_at AS linked_at
FROM risk_aggregations ra
JOIN risks r ON r.tenant_id = ra.tenant_id AND r.id = ra.child_risk_id
WHERE ra.tenant_id = $1 AND ra.parent_risk_id = $2
ORDER BY ra.created_at ASC, r.id ASC;

-- name: ListRiskParentLinks :many
-- The parents a risk rolls up to, same shape as ListRiskChildLinks (#1748).
SELECT r.id, r.title, r.level, r.org_unit_id, r.methodology, r.inherent_score,
       ra.rule_id, ra.created_at AS linked_at
FROM risk_aggregations ra
JOIN risks r ON r.tenant_id = ra.tenant_id AND r.id = ra.parent_risk_id
WHERE ra.tenant_id = $1 AND ra.child_risk_id = $2
ORDER BY ra.created_at ASC, r.id ASC;

-- name: DeleteRiskAggregationLink :execrows
-- Detach one child from one parent, reporting how many rows went, so the
-- caller can tell a removed link from one that never existed (#1748).
DELETE FROM risk_aggregations
WHERE tenant_id = $1 AND parent_risk_id = $2 AND child_risk_id = $3;

-- name: ListRiskAggregationLinksForTenant :many
-- Every parent link in the tenant, for the risk register export's
-- parent_risk_ids column (#1748): one query, folded per child in Go.
SELECT child_risk_id, parent_risk_id
FROM risk_aggregations
WHERE tenant_id = $1
ORDER BY child_risk_id ASC, created_at ASC, parent_risk_id ASC;
