-- Decision Log link tables — four separate M:N tables for risks, controls,
-- exceptions, and framework scope predicates (canvas §6.7). Each link is
-- idempotent (ON CONFLICT DO NOTHING) so re-linking is a no-op.

-- ===== decision_risks =====

-- name: LinkDecisionRisk :exec
INSERT INTO decision_risks (decision_id, target_id, tenant_id)
VALUES ($1, $2, $3)
ON CONFLICT (tenant_id, decision_id, target_id) DO NOTHING;

-- name: UnlinkDecisionRisk :exec
DELETE FROM decision_risks
WHERE tenant_id = $1 AND decision_id = $2 AND target_id = $3;

-- name: ListDecisionRisks :many
SELECT target_id, created_at
FROM decision_risks
WHERE tenant_id = $1 AND decision_id = $2
ORDER BY created_at ASC, target_id ASC;

-- name: ListDecisionsForRisk :many
SELECT decision_id, created_at
FROM decision_risks
WHERE tenant_id = $1 AND target_id = $2
ORDER BY created_at ASC, decision_id ASC;

-- ===== decision_controls =====

-- name: LinkDecisionControl :exec
INSERT INTO decision_controls (decision_id, target_id, tenant_id)
VALUES ($1, $2, $3)
ON CONFLICT (tenant_id, decision_id, target_id) DO NOTHING;

-- name: UnlinkDecisionControl :exec
DELETE FROM decision_controls
WHERE tenant_id = $1 AND decision_id = $2 AND target_id = $3;

-- name: ListDecisionControls :many
SELECT target_id, created_at
FROM decision_controls
WHERE tenant_id = $1 AND decision_id = $2
ORDER BY created_at ASC, target_id ASC;

-- name: ListDecisionsForControl :many
SELECT decision_id, created_at
FROM decision_controls
WHERE tenant_id = $1 AND target_id = $2
ORDER BY created_at ASC, decision_id ASC;

-- ===== decision_exceptions =====

-- name: LinkDecisionException :exec
INSERT INTO decision_exceptions (decision_id, target_id, tenant_id)
VALUES ($1, $2, $3)
ON CONFLICT (tenant_id, decision_id, target_id) DO NOTHING;

-- name: UnlinkDecisionException :exec
DELETE FROM decision_exceptions
WHERE tenant_id = $1 AND decision_id = $2 AND target_id = $3;

-- name: ListDecisionExceptions :many
SELECT target_id, created_at
FROM decision_exceptions
WHERE tenant_id = $1 AND decision_id = $2
ORDER BY created_at ASC, target_id ASC;

-- name: ListDecisionsForException :many
SELECT decision_id, created_at
FROM decision_exceptions
WHERE tenant_id = $1 AND target_id = $2
ORDER BY created_at ASC, decision_id ASC;

-- ===== decision_scope_predicates =====

-- name: LinkDecisionScopePredicate :exec
INSERT INTO decision_scope_predicates (decision_id, target_id, tenant_id)
VALUES ($1, $2, $3)
ON CONFLICT (tenant_id, decision_id, target_id) DO NOTHING;

-- name: UnlinkDecisionScopePredicate :exec
DELETE FROM decision_scope_predicates
WHERE tenant_id = $1 AND decision_id = $2 AND target_id = $3;

-- name: ListDecisionScopePredicates :many
SELECT target_id, created_at
FROM decision_scope_predicates
WHERE tenant_id = $1 AND decision_id = $2
ORDER BY created_at ASC, target_id ASC;
