-- name: CreateRisk :one
-- Insert a new risk. The application validates methodology-specific
-- inherent_score and per-treatment required fields BEFORE calling this.
-- DB-side CHECK constraints (slice 019) are defense-in-depth, not the
-- primary validation path.
INSERT INTO risks (
    id, tenant_id, title, description, category, methodology,
    inherent_score, treatment, treatment_owner, residual_score,
    review_due_at, accepted_until, accepter, instrument_reference
)
VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10,
    $11, $12, $13, $14
)
RETURNING *;

-- name: GetRiskByID :one
SELECT *
FROM risks
WHERE tenant_id = $1 AND id = $2;

-- name: ListRisks :many
-- Enumerate all risks for the tenant, newest first. Filters are applied
-- in the application layer because sqlc's static typing makes optional
-- WHERE clauses noisy; the row count is bounded by tenant-size anyway.
SELECT *
FROM risks
WHERE tenant_id = $1
ORDER BY created_at DESC, id ASC;

-- name: UpdateRisk :one
-- Full-update path (PATCH handler reads existing, mutates fields, writes).
-- updated_at is set explicitly so the schema's per-row default doesn't
-- silently keep stale values.
UPDATE risks
SET title = $3,
    description = $4,
    category = $5,
    methodology = $6,
    inherent_score = $7,
    treatment = $8,
    treatment_owner = $9,
    residual_score = $10,
    review_due_at = $11,
    accepted_until = $12,
    accepter = $13,
    instrument_reference = $14,
    updated_at = now()
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: DeleteRisk :exec
DELETE FROM risks
WHERE tenant_id = $1 AND id = $2;

-- name: LinkRiskControl :exec
-- Idempotent: ON CONFLICT DO NOTHING so re-running a "link these controls"
-- request does not 23505 on a re-link.
INSERT INTO risk_control_links (risk_id, control_id, tenant_id)
VALUES ($1, $2, $3)
ON CONFLICT (tenant_id, risk_id, control_id) DO NOTHING;

-- name: UnlinkRiskControl :exec
DELETE FROM risk_control_links
WHERE tenant_id = $1 AND risk_id = $2 AND control_id = $3;

-- name: ListRiskControlLinks :many
-- Returns all control links for a single risk.
SELECT control_id, created_at
FROM risk_control_links
WHERE tenant_id = $1 AND risk_id = $2
ORDER BY created_at ASC, control_id ASC;

-- name: CountRiskControlLinks :one
SELECT COUNT(*)::bigint
FROM risk_control_links
WHERE tenant_id = $1 AND risk_id = $2;

-- name: HeatmapBuckets :many
-- Returns risk counts grouped by (likelihood, impact) for risks whose
-- methodology shares the 5x5 (likelihood, impact 1..5) shape. nist_800_30
-- and qualitative_5x5 both use this shape (canvas §2.2 + AC-7); other
-- methodologies (FAIR has LEF/LM) are intentionally excluded so the
-- heatmap stays a single coherent visualization.
--
-- The CAST chain (jsonb -> text -> int) is necessary because pgx cannot
-- read jsonb-number values directly as int4 without an explicit cast.
SELECT
    (inherent_score->>'likelihood')::int  AS likelihood,
    (inherent_score->>'impact')::int      AS impact,
    COUNT(*)::int                         AS count
FROM risks
WHERE tenant_id = $1
  AND methodology IN ('nist_800_30', 'qualitative_5x5')
  AND jsonb_typeof(inherent_score->'likelihood') = 'number'
  AND jsonb_typeof(inherent_score->'impact')     = 'number'
  AND (inherent_score->>'likelihood')::int BETWEEN 1 AND 5
  AND (inherent_score->>'impact')::int     BETWEEN 1 AND 5
GROUP BY likelihood, impact
ORDER BY likelihood, impact;
