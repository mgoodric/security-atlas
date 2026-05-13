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
