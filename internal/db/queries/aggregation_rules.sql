-- Slice 054 — declarative aggregation rules engine queries.
--
-- All queries are tenant-scoped via the (tenant_id, ...) prefix; RLS is the
-- defense-in-depth layer and the WHERE clauses are the primary correctness
-- guarantee (canvas invariant #6). The two log tables (evaluations,
-- audit_log) have SELECT + INSERT policies only under FORCE RLS so no
-- UPDATE/DELETE path exists — append-only by construction.
--
-- The engine's hot path is ListActiveAggregationRules + the candidate-risk
-- read; both run on every risk write inside the SAME tenant transaction as
-- the risk INSERT/UPDATE/DELETE.

-- name: CreateAggregationRule :one
-- Insert a new rule. ALWAYS lands as 'staged' — the HITL gate. The
-- application validates the rule body against the JSON Schema BEFORE
-- calling; the DB CHECK constraints are defense-in-depth. activated_by /
-- activated_at are left NULL (the activation-coherent CHECK requires this
-- for status='staged').
INSERT INTO aggregation_rules (
    id, tenant_id, rule_id, target_theme,
    min_risks, min_teams, window_days,
    parent_level, severity_function, rule_body,
    status
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'staged')
RETURNING *;

-- name: GetAggregationRuleByID :one
SELECT *
FROM aggregation_rules
WHERE tenant_id = $1 AND id = $2;

-- name: GetAggregationRuleByRuleID :one
-- Lookup by the human-authored rule_id (the canvas §6.6 identifier).
SELECT *
FROM aggregation_rules
WHERE tenant_id = $1 AND rule_id = $2;

-- name: ListAggregationRules :many
-- Every rule for the tenant, newest first. The handler applies the
-- optional status filter in-memory; cardinality is small (a solo security
-- lead runs a handful of rules).
SELECT *
FROM aggregation_rules
WHERE tenant_id = $1
ORDER BY created_at DESC, id ASC;

-- name: ListActiveAggregationRules :many
-- The engine's hot path: every 'active' rule for the tenant. Runs on every
-- risk write. Hits idx_aggregation_rules_tenant_status.
SELECT *
FROM aggregation_rules
WHERE tenant_id = $1 AND status = 'active'
ORDER BY created_at ASC, id ASC;

-- name: ActivateAggregationRule :one
-- HITL transition: staged -> active OR inactive -> active (reactivation).
-- Sets activated_by + activated_at; the engine reads activated_at as the
-- "do not consider risks older than this" cut-off so re-activation never
-- re-fires on stale data (anti-criterion P0). The WHERE status guard
-- refuses the transition from an unexpected state (zero rows returned ->
-- the handler probes to disambiguate missing-row vs wrong-state).
UPDATE aggregation_rules
SET status = 'active',
    activated_by = $3,
    activated_at = now(),
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status IN ('staged', 'inactive')
RETURNING *;

-- name: DeactivateAggregationRule :one
-- Transition: active -> inactive. Stops new firings; historical meta-risks
-- are untouched. activated_by / activated_at are intentionally PRESERVED
-- (not cleared) so the activation-coherent CHECK still holds for an
-- inactive row and the audit story stays legible.
UPDATE aggregation_rules
SET status = 'inactive',
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = 'active'
RETURNING *;

-- name: UpdateAggregationRuleThresholds :one
-- Edit the threshold fields + the rule body. Does NOT touch status or
-- activation metadata — a threshold edit is not a lifecycle transition and
-- (per the issue's notes) must NOT retroactively re-fire on pre-edit data.
-- The application writes a 'threshold_changed' aggregation_rule_audit_log
-- row alongside this call.
UPDATE aggregation_rules
SET target_theme = $3,
    min_risks = $4,
    min_teams = $5,
    window_days = $6,
    parent_level = $7,
    severity_function = $8,
    rule_body = $9,
    updated_at = now()
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: DeleteAggregationRule :exec
DELETE FROM aggregation_rules
WHERE tenant_id = $1 AND id = $2;

-- ===== candidate-risk read (engine) =====

-- name: ListCandidateRisksForRule :many
-- The engine's candidate-risk query. Returns risks for the tenant that:
--   - carry the rule's target_theme (the text[] column `themes` contains
--     the scalar `target_theme` — `@>` with a one-element array literal),
--   - were created on/after the window cut-off (`window_start` — the later
--     of the window start and the rule's activated_at, computed in Go), and
--   - use an aggregation-eligible methodology so slice 053's severity
--     functions can read a comparable (likelihood, impact) scalar.
-- Hits idx_risks_tenant_created_themes for the (tenant_id, created_at)
-- range scan, then the GIN-backed theme containment narrows the slice.
-- The methodology filter mirrors slice 053's IsAggregableMethodology.
--
-- target_theme is a SCALAR text parameter — the explicit `::text` cast on
-- sqlc.arg keeps sqlc from inferring it as text[] just because it appears
-- inside an ARRAY[] constructor.
SELECT *
FROM risks
WHERE tenant_id = $1
  AND themes @> ARRAY[sqlc.arg(target_theme)::text]
  AND created_at >= sqlc.arg(window_start)
  AND methodology IN ('nist_800_30', 'qualitative_5x5')
ORDER BY created_at ASC, id ASC;

-- name: GetRuleMetaRiskByKey :one
-- Idempotency lookup: find the meta-risk a rule already created for a
-- given (rule_id, window_start) window. The key is stored on the
-- meta-risk's inherent_score JSONB as 'aggregation_key' (slice 053
-- pattern). If found, the engine UPDATEs it rather than creating a
-- duplicate (canvas §6.6: one meta-risk per rule per window).
SELECT *
FROM risks
WHERE tenant_id = $1
  AND inherent_score->>'aggregation_key' = $2::text
LIMIT 1;

-- name: UpdateMetaRiskInherentScore :one
-- Recompute path: the engine recomputes the meta-risk's severity blob when
-- new children join the window. Only inherent_score is touched — title,
-- level, treatment, and lifecycle are frozen at create time (canvas §6.6:
-- closing a child never auto-closes the parent; the engine never mutates
-- parent lifecycle).
UPDATE risks
SET inherent_score = $3,
    updated_at = now()
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- ===== aggregation_rule_evaluations (append-only) =====

-- name: WriteAggregationRuleEvaluation :one
-- Append-only. EVERY evaluation cycle writes exactly one row, including
-- 'no_match' (canvas §6.6 + AC-8: the audit trail proves the engine ran).
-- window_start + meta_risk_id are non-NULL only for outcome='fired' (the
-- fired-coherent CHECK enforces this).
INSERT INTO aggregation_rule_evaluations (
    id, tenant_id, rule_id, outcome,
    risk_count, team_count, window_start, meta_risk_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListAggregationRuleEvaluations :many
-- Per-rule evaluation history, newest first — the auditor view. Hits
-- idx_aggregation_rule_evaluations_tenant_rule.
SELECT *
FROM aggregation_rule_evaluations
WHERE tenant_id = $1 AND rule_id = $2
ORDER BY evaluated_at DESC, id ASC;

-- name: GetLastFiredEvaluation :one
-- The engine's "when did this rule last fire, and into which window"
-- lookup. Used to decide whether the current write falls inside an
-- existing window. Returns the most recent 'fired' row for the rule.
SELECT *
FROM aggregation_rule_evaluations
WHERE tenant_id = $1
  AND rule_id = $2
  AND outcome = 'fired'
ORDER BY evaluated_at DESC, id ASC
LIMIT 1;

-- ===== aggregation_rule_audit_log (append-only) =====

-- name: WriteAggregationRuleAuditLog :one
-- Append-only. Every lifecycle transition (created / activated /
-- deactivated / reactivated) and every threshold edit writes one row
-- naming the human actor. The HITL gate's evidence trail.
--
-- Slice 180: explicit `subject_module='core'` (column defaults to 'core' at
-- the DB layer; explicit-is-clearer per AC-5).
INSERT INTO aggregation_rule_audit_log (
    id, tenant_id, rule_id, event,
    actor, from_status, to_status, detail, subject_module
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'core')
RETURNING *;

-- name: ListAggregationRuleAuditLog :many
SELECT *
FROM aggregation_rule_audit_log
WHERE tenant_id = $1 AND rule_id = $2
ORDER BY created_at DESC, id ASC;
