-- name: CreateRisk :one
-- Insert a new risk. The application validates methodology-specific
-- inherent_score and per-treatment required fields BEFORE calling this.
-- DB-side CHECK constraints (slice 019) are defense-in-depth, not the
-- primary validation path.
INSERT INTO risks (
    id, tenant_id, title, description, category_id, methodology,
    inherent_score, treatment, treatment_owner, residual_score,
    review_due_at, accepted_until, accepter, instrument_reference,
    acceptance_status, level, org_unit_id,
    acceptance_proposed_by, acceptance_proposed_at, owner_user_id
)
VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10,
    $11, $12, $13, $14,
    $15, $16, $17,
    $18, $19, $20
)
RETURNING *;

-- name: GetRiskByID :one
SELECT *
FROM risks
WHERE tenant_id = $1 AND id = $2;

-- name: LockRiskByID :one
-- #1867: read a risk and hold its row against concurrent theme writes and
-- deletes until the transaction ends, so a theme write or delete sees the
-- themes it actually replaces. FOR NO KEY UPDATE, not FOR UPDATE: it does
-- not conflict with the FOR KEY SHARE an aggregation evaluation takes when
-- it links this risk as a child, so an evaluation is never blocked by it.
SELECT *
FROM risks
WHERE tenant_id = $1 AND id = $2
FOR NO KEY UPDATE;

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
-- silently keep stale values. The category moves only through
-- UpdateRiskCategory, which the caller audit-logs (#1812).
UPDATE risks
SET title = $3,
    description = $4,
    methodology = $5,
    inherent_score = $6,
    treatment = $7,
    treatment_owner = $8,
    residual_score = $9,
    review_due_at = $10,
    accepted_until = $11,
    accepter = $12,
    instrument_reference = $13,
    acceptance_status = $14,
    owner_user_id = $15,
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

-- name: ListRiskControlLinkWeights :many
-- Slice 020: returns each control link for a risk WITH the per-link
-- effectiveness weighting columns (migration `_029`). The residual derivation
-- reads design_score + the three weights here; operational_score and
-- coverage_score are computed at read time from the evaluation ledger and are
-- never stored on the link row (caching a derived score beyond its staleness
-- threshold is a P0 anti-criterion).
SELECT control_id, design_score, weight_design, weight_operation,
       weight_coverage, created_at
FROM risk_control_links
WHERE tenant_id = $1 AND risk_id = $2
ORDER BY created_at ASC, control_id ASC;

-- name: GetRiskControlLink :one
-- Slice 020: one link row by (risk, control). Used to confirm a link exists
-- before returning its effectiveness breakdown.
SELECT control_id, design_score, weight_design, weight_operation,
       weight_coverage, created_at
FROM risk_control_links
WHERE tenant_id = $1 AND risk_id = $2 AND control_id = $3;

-- name: LinkRiskControlWithWeights :exec
-- Slice 020: link a control to a risk with explicit effectiveness weights.
-- Idempotent: ON CONFLICT updates the weights so a re-link with new weights
-- is an update, not a 23505. The slice-019 LinkRiskControl (no weights) stays
-- for the create-risk path — it relies on the column DEFAULTs.
INSERT INTO risk_control_links (
    risk_id, control_id, tenant_id,
    design_score, weight_design, weight_operation, weight_coverage
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (tenant_id, risk_id, control_id) DO UPDATE
SET design_score     = EXCLUDED.design_score,
    weight_design    = EXCLUDED.weight_design,
    weight_operation = EXCLUDED.weight_operation,
    weight_coverage  = EXCLUDED.weight_coverage;

-- name: ListRiskIDsLinkedToControl :many
-- Slice 020: every risk in the tenant that links the given control. The
-- evidence-ingest residual subscriber uses this to find which risks must be
-- recomputed when a control's state changes.
SELECT risk_id
FROM risk_control_links
WHERE tenant_id = $1 AND control_id = $2
ORDER BY risk_id ASC;

-- name: UpdateRiskResidual :one
-- Slice 020: writes the freshly-derived residual_score JSONB onto a risk.
-- This is the ONLY column the residual derivation mutates — it never touches
-- inherent_score, never touches evidence (constitutional invariant #2). The
-- $3 parameter is cast to jsonb explicitly so sqlc does not borrow the
-- bytea-via-JSONB inference and force an awkward caller type.
UPDATE risks
SET residual_score = sqlc.arg(residual_score)::jsonb,
    updated_at = now()
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: UpdateRiskThemes :one
-- Replaces the themes array on a risk. The application validates that every
-- supplied theme is in the visible vocabulary (defaults + tenant-private)
-- BEFORE calling. Returns the updated row. Slice 053 (POST/DELETE
-- /v1/risks/{id}/themes).
UPDATE risks
SET themes = $3::text[],
    updated_at = now()
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: GetRiskByAggregationKey :one
-- Lookup an existing parent risk by the sha256 idempotency key stored on
-- inherent_score. Used by slice 053's POST /v1/risks/aggregate to satisfy
-- AC-7: same (parent_title, child_set) returns the existing parent
-- rather than creating a duplicate.
--
-- The $2::text cast pins the parameter type to text (sqlc's inference
-- otherwise borrows the type of `inherent_score`, which is bytea via JSONB
-- and would force the caller to pass []byte).
SELECT *
FROM risks
WHERE tenant_id = $1
  AND inherent_score->>'aggregation_key' = $2::text
LIMIT 1;

-- name: ListRisksByIDs :many
-- Reads a set of risks by id for the active tenant. RLS makes cross-tenant
-- rows invisible; the caller compares len(returned)==len(requested) for the
-- existence check.
SELECT *
FROM risks
WHERE tenant_id = $1 AND id = ANY($2::uuid[])
ORDER BY id ASC;

-- name: CreateAggregateRisk :one
-- Insert a parent risk for slice 053 manual aggregation. The shape mirrors
-- CreateRisk but with `level`, `org_unit_id`, and `themes` plumbed through —
-- those columns exist on `risks` per slice 052's ALTER. The aggregated
-- severity, severity_function, child_count, and aggregation_key live inside
-- the `inherent_score` JSONB.
INSERT INTO risks (
    id, tenant_id, title, description, category_id, methodology,
    inherent_score, treatment, treatment_owner, residual_score,
    accepter, instrument_reference, level, org_unit_id, themes
)
VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10,
    $11, $12, $13, $14, $15::text[]
)
RETURNING *;

-- name: HeatmapBuckets :many
-- Returns risk counts grouped by (likelihood, impact) for risks whose
-- methodology shares the 5x5 (likelihood, impact 1..5) shape. nist_800_30
-- and qualitative_5x5 both use this shape (canvas §2.2 + AC-7); other
-- methodologies (FAIR has LEF/LM) are intentionally excluded so the
-- heatmap stays a single coherent visualization. Closed risks are left
-- out (#2107): the heatmap counts live exposure.
--
-- The CAST chain (jsonb -> text -> int) is necessary because pgx cannot
-- read jsonb-number values directly as int4 without an explicit cast.
SELECT
    (inherent_score->>'likelihood')::int  AS likelihood,
    (inherent_score->>'impact')::int      AS impact,
    COUNT(*)::int                         AS count
FROM risks
WHERE tenant_id = $1
  AND status = 'open'
  AND methodology IN ('nist_800_30', 'qualitative_5x5')
  AND jsonb_typeof(inherent_score->'likelihood') = 'number'
  AND jsonb_typeof(inherent_score->'impact')     = 'number'
  AND (inherent_score->>'likelihood')::int BETWEEN 1 AND 5
  AND (inherent_score->>'impact')::int     BETWEEN 1 AND 5
GROUP BY likelihood, impact
ORDER BY likelihood, impact;

-- name: UpdateRiskPlacement :one
-- #1749: move a risk between hierarchy levels and org units. The caller
-- holds the row via LockRiskByID and has checked that level matches the
-- org unit's level.
UPDATE risks
SET level = $3,
    org_unit_id = $4,
    updated_at = now()
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: UpdateRiskCategory :one
-- #1812: move a risk onto another of the tenant's categories. The caller
-- holds the row via LockRiskByID and has resolved the category as
-- assignable.
UPDATE risks
SET category_id = sqlc.arg(category_id),
    updated_at = now()
WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: InsertRiskAuditLog :exec
-- #1749: one append-only row per placement change, in the write's tx.
INSERT INTO risk_audit_log (
    tenant_id, risk_id, actor, action_type, before_state, after_state
)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListRiskAuditLog :many
SELECT *
FROM risk_audit_log
WHERE tenant_id = $1 AND risk_id = $2
ORDER BY created_at DESC, id DESC;

-- name: GetRiskReviewCadenceDays :one
-- #2059: the tenant's risk review cadence in days, as the
-- risks_review_due_default trigger reads it: the tenants row's
-- risk_review_cadence_days, or 365 for a tenant with no row (a bare-UUID
-- tenant predating slice 144). RLS scopes tenants to the caller's own row.
SELECT COALESCE(
    (SELECT t.risk_review_cadence_days FROM tenants t WHERE t.id = $1),
    365
)::int AS cadence_days;

-- name: SetRiskTreatment :one
-- #2105: the treatment and acceptance columns, written only by the risk
-- treatment and acceptance transitions (internal/risk/transitions.go),
-- which validate the state machine and audit-log the change in the same
-- transaction. Record fields are untouched.
UPDATE risks
SET treatment = $3,
    acceptance_status = $4,
    instrument_reference = $5,
    accepted_until = $6,
    accepter = $7,
    acceptance_justification = $8,
    acceptance_proposed_until = $9,
    acceptance_proposed_by = $10,
    acceptance_proposed_at = $11,
    acceptance_approved_at = $12,
    self_approved = $13,
    updated_at = now()
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: CountRiskActionPlanLinks :one
-- #2105: live (not tombstoned) action plans linked to a risk, for the
-- mitigate rule (a linked control or a linked action plan).
SELECT COUNT(*)::bigint
FROM action_plan_risks apr
JOIN action_plans ap
  ON ap.id = apr.action_plan_id AND ap.tenant_id = apr.tenant_id
WHERE apr.tenant_id = $1 AND apr.risk_id = $2
  AND ap.tombstoned_at IS NULL;

-- name: SetRiskStatus :one
-- #2107: the lifecycle columns, written only by the close and reopen
-- transitions (internal/risk/lifecycle.go), which check the current state
-- and audit-log the change in the same transaction.
UPDATE risks
SET status = sqlc.arg(status),
    closure_reason = sqlc.narg(closure_reason),
    closure_note = sqlc.arg(closure_note),
    closed_at = sqlc.narg(closed_at),
    closed_by = sqlc.arg(closed_by),
    updated_at = now()
WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: ListRisksPage :many
-- #2132: the risk register, filtered, sorted and paged on the server.
--
-- Filters (each optional; NULL / false leaves it off):
--   status            open | closed. NULL keeps both.
--   include_rollups   false leaves out a roll-up: a risk with at least one
--                     open child (#2060, the #2107 open-children rule).
--   treatment, methodology, level, theme, org_unit_id: exact match.
--   category_ids      any of these organisation categories (#1745); the
--                     store resolves slugs to ids first.
--   severity_min, severity_max  the assessed severity in that inclusive
--                     range (#2490), FAIR left out. The value is
--                     risk.AssessedSeverity's, the wire `severity`: the
--                     inherent score's `severity`, else likelihood x
--                     impact, each truncated to a whole number, and 0 when
--                     the score is not an object or any of the three keys
--                     is not a number (Go's json.Unmarshal into *float64
--                     fails on the whole object then). The store sets the
--                     bounds from risk.SeverityBandRange.
--   owner_user_id     that person's risks (#2129); unowned keeps the risks
--                     with no owner user.
--   due_from, due_before  review_due_at in [due_from, due_before). The
--                     store turns overdue, due-soon and a date range into
--                     these two UTC-day bounds (the duedate rule).
--
-- sort_key is one of the fixed keys in the ORDER BY; the handler refuses
-- any other value before it gets here. Residual follows #2185's
-- risk.RanksAbove and risk.Rank.Tier: scored 5x5 risks, then scored FAIR
-- risks, then unscored risks of any methodology (#2516), in either
-- direction, so a dollar figure never ranks against a 1..25 score. The
-- value is risk.ScoreValue's: score, then severity, then likelihood x
-- impact; a risk with no such value is unscored. Every order ends on r.id,
-- so offset paging has no duplicates or gaps. Text keys sort lower-cased
-- under COLLATE "C", so the order is the same on every deployment.
--
-- RLS posture: the explicit tenant_id predicates sit alongside the
-- GUC-driven RLS on every table read here.
SELECT
    sqlc.embed(r),
    kids.child_count::int AS child_count
FROM risks r
LEFT JOIN LATERAL (
    SELECT COUNT(*)::int AS child_count
    FROM risk_aggregations ra
    JOIN risks c ON c.tenant_id = ra.tenant_id AND c.id = ra.child_risk_id
    WHERE ra.tenant_id = r.tenant_id
      AND ra.parent_risk_id = r.id
      AND c.status = 'open'
) kids ON true
LEFT JOIN org_risk_categories cat
       ON cat.tenant_id = r.tenant_id AND cat.id = r.category_id
LEFT JOIN users ou
       ON ou.tenant_id = r.tenant_id AND ou.id = r.owner_user_id
LEFT JOIN org_units unit
       ON unit.tenant_id = r.tenant_id AND unit.id = r.org_unit_id
LEFT JOIN LATERAL (
    SELECT CASE WHEN r.methodology = 'fair' THEN 1 ELSE 0 END AS fair_group,
           CASE
               WHEN jsonb_typeof(r.residual_score -> 'score') = 'number'
                   THEN (r.residual_score ->> 'score')::float8
               WHEN jsonb_typeof(r.residual_score -> 'severity') = 'number'
                   THEN (r.residual_score ->> 'severity')::float8
               WHEN jsonb_typeof(r.residual_score -> 'likelihood') = 'number'
                AND jsonb_typeof(r.residual_score -> 'impact') = 'number'
                   THEN (r.residual_score ->> 'likelihood')::float8 * (r.residual_score ->> 'impact')::float8
           END AS residual,
           lower(COALESCE(NULLIF(ou.display_name, ''), NULLIF(ou.email, ''), NULLIF(btrim(r.treatment_owner), ''))) AS owner_name
) k ON true
LEFT JOIN LATERAL (
    SELECT CASE
               WHEN jsonb_typeof(r.inherent_score) <> 'object'
                 OR COALESCE(jsonb_typeof(r.inherent_score -> 'severity'), 'null') NOT IN ('number', 'null')
                 OR COALESCE(jsonb_typeof(r.inherent_score -> 'likelihood'), 'null') NOT IN ('number', 'null')
                 OR COALESCE(jsonb_typeof(r.inherent_score -> 'impact'), 'null') NOT IN ('number', 'null')
                   THEN 0
               WHEN jsonb_typeof(r.inherent_score -> 'severity') = 'number'
                   THEN trunc((r.inherent_score ->> 'severity')::numeric)
               WHEN jsonb_typeof(r.inherent_score -> 'likelihood') = 'number'
                AND jsonb_typeof(r.inherent_score -> 'impact') = 'number'
                   THEN trunc((r.inherent_score ->> 'likelihood')::numeric) * trunc((r.inherent_score ->> 'impact')::numeric)
               ELSE 0
           END AS assessed
) sev ON true
WHERE r.tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg('status')::text IS NULL OR r.status::text = sqlc.narg('status')::text)
  AND (sqlc.arg(include_rollups)::bool OR kids.child_count = 0)
  AND (sqlc.narg('treatment')::text IS NULL OR r.treatment::text = sqlc.narg('treatment')::text)
  AND (sqlc.narg('methodology')::text IS NULL OR r.methodology::text = sqlc.narg('methodology')::text)
  AND (sqlc.narg('level')::text IS NULL OR r.level::text = sqlc.narg('level')::text)
  AND (sqlc.narg('theme')::text IS NULL OR sqlc.narg('theme')::text = ANY(r.themes))
  AND (sqlc.narg('org_unit_id')::uuid IS NULL OR r.org_unit_id = sqlc.narg('org_unit_id')::uuid)
  AND (sqlc.narg('category_ids')::uuid[] IS NULL OR r.category_id = ANY(sqlc.narg('category_ids')::uuid[]))
  AND (sqlc.narg('owner_user_id')::uuid IS NULL OR r.owner_user_id = sqlc.narg('owner_user_id')::uuid)
  AND (NOT sqlc.arg(unowned)::bool OR r.owner_user_id IS NULL)
  AND (sqlc.narg('due_from')::timestamptz IS NULL OR r.review_due_at >= sqlc.narg('due_from')::timestamptz)
  AND (sqlc.narg('due_before')::timestamptz IS NULL OR r.review_due_at < sqlc.narg('due_before')::timestamptz)
  AND (sqlc.narg('severity_min')::int IS NULL OR (r.methodology <> 'fair' AND sev.assessed >= sqlc.narg('severity_min')::int))
  AND (sqlc.narg('severity_max')::int IS NULL OR sev.assessed <= sqlc.narg('severity_max')::int)
ORDER BY
    -- residual (the default, descending): 5x5, then FAIR, then unscored,
    -- in both directions (risk.Rank.Tier).
    CASE WHEN sqlc.arg(sort_key)::text IN ('residual', 'residual_age') THEN
        CASE WHEN k.residual IS NULL THEN 2 ELSE k.fair_group END
    END ASC,
    CASE WHEN sqlc.arg(sort_key)::text = 'residual' AND NOT sqlc.arg(sort_desc)::bool THEN k.residual END ASC NULLS LAST,
    CASE WHEN (sqlc.arg(sort_key)::text = 'residual' AND sqlc.arg(sort_desc)::bool) OR sqlc.arg(sort_key)::text = 'residual_age' THEN k.residual END DESC NULLS LAST,
    -- residual_age (the dashboard's residual,age): oldest first on a tie.
    CASE WHEN sqlc.arg(sort_key)::text = 'residual_age' THEN r.created_at END ASC,
    CASE WHEN sqlc.arg(sort_key)::text = 'title' AND NOT sqlc.arg(sort_desc)::bool THEN lower(r.title) COLLATE "C" END ASC,
    CASE WHEN sqlc.arg(sort_key)::text = 'title' AND sqlc.arg(sort_desc)::bool THEN lower(r.title) COLLATE "C" END DESC,
    CASE WHEN sqlc.arg(sort_key)::text = 'category' AND NOT sqlc.arg(sort_desc)::bool THEN lower(cat.name) COLLATE "C" END ASC NULLS LAST,
    CASE WHEN sqlc.arg(sort_key)::text = 'category' AND sqlc.arg(sort_desc)::bool THEN lower(cat.name) COLLATE "C" END DESC NULLS LAST,
    CASE WHEN sqlc.arg(sort_key)::text = 'owner' AND NOT sqlc.arg(sort_desc)::bool THEN k.owner_name COLLATE "C" END ASC NULLS LAST,
    CASE WHEN sqlc.arg(sort_key)::text = 'owner' AND sqlc.arg(sort_desc)::bool THEN k.owner_name COLLATE "C" END DESC NULLS LAST,
    CASE WHEN sqlc.arg(sort_key)::text = 'treatment' AND NOT sqlc.arg(sort_desc)::bool THEN r.treatment::text COLLATE "C" END ASC,
    CASE WHEN sqlc.arg(sort_key)::text = 'treatment' AND sqlc.arg(sort_desc)::bool THEN r.treatment::text COLLATE "C" END DESC,
    CASE WHEN sqlc.arg(sort_key)::text = 'review_due' AND NOT sqlc.arg(sort_desc)::bool THEN r.review_due_at END ASC NULLS LAST,
    CASE WHEN sqlc.arg(sort_key)::text = 'review_due' AND sqlc.arg(sort_desc)::bool THEN r.review_due_at END DESC NULLS LAST,
    CASE WHEN sqlc.arg(sort_key)::text = 'status' AND NOT sqlc.arg(sort_desc)::bool THEN r.status::text COLLATE "C" END ASC,
    CASE WHEN sqlc.arg(sort_key)::text = 'status' AND sqlc.arg(sort_desc)::bool THEN r.status::text COLLATE "C" END DESC,
    CASE WHEN sqlc.arg(sort_key)::text = 'methodology' AND NOT sqlc.arg(sort_desc)::bool THEN r.methodology::text COLLATE "C" END ASC,
    CASE WHEN sqlc.arg(sort_key)::text = 'methodology' AND sqlc.arg(sort_desc)::bool THEN r.methodology::text COLLATE "C" END DESC,
    CASE WHEN sqlc.arg(sort_key)::text = 'level' AND NOT sqlc.arg(sort_desc)::bool THEN r.level END ASC,
    CASE WHEN sqlc.arg(sort_key)::text = 'level' AND sqlc.arg(sort_desc)::bool THEN r.level END DESC,
    CASE WHEN sqlc.arg(sort_key)::text = 'org_unit' AND NOT sqlc.arg(sort_desc)::bool THEN lower(unit.name) COLLATE "C" END ASC NULLS LAST,
    CASE WHEN sqlc.arg(sort_key)::text = 'org_unit' AND sqlc.arg(sort_desc)::bool THEN lower(unit.name) COLLATE "C" END DESC NULLS LAST,
    CASE WHEN sqlc.arg(sort_key)::text = 'themes' AND NOT sqlc.arg(sort_desc)::bool THEN lower(array_to_string(r.themes, ',')) COLLATE "C" END ASC,
    CASE WHEN sqlc.arg(sort_key)::text = 'themes' AND sqlc.arg(sort_desc)::bool THEN lower(array_to_string(r.themes, ',')) COLLATE "C" END DESC,
    CASE WHEN sqlc.arg(sort_key)::text = 'created_at' AND NOT sqlc.arg(sort_desc)::bool THEN r.created_at END ASC,
    CASE WHEN sqlc.arg(sort_key)::text = 'created_at' AND sqlc.arg(sort_desc)::bool THEN r.created_at END DESC,
    CASE WHEN sqlc.arg(sort_key)::text = 'id' AND sqlc.arg(sort_desc)::bool THEN r.id END DESC,
    r.id
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: CountRisksPage :one
-- #2132: the total for ListRisksPage. It takes every filter the list does,
-- so the total counts exactly the set the register pages over.
SELECT count(*)::bigint AS total
FROM risks r
LEFT JOIN LATERAL (
    SELECT COUNT(*)::int AS child_count
    FROM risk_aggregations ra
    JOIN risks c ON c.tenant_id = ra.tenant_id AND c.id = ra.child_risk_id
    WHERE ra.tenant_id = r.tenant_id
      AND ra.parent_risk_id = r.id
      AND c.status = 'open'
) kids ON true
LEFT JOIN LATERAL (
    -- The same expression as ListRisksPage's sev lateral; keep them equal.
    SELECT CASE
               WHEN jsonb_typeof(r.inherent_score) <> 'object'
                 OR COALESCE(jsonb_typeof(r.inherent_score -> 'severity'), 'null') NOT IN ('number', 'null')
                 OR COALESCE(jsonb_typeof(r.inherent_score -> 'likelihood'), 'null') NOT IN ('number', 'null')
                 OR COALESCE(jsonb_typeof(r.inherent_score -> 'impact'), 'null') NOT IN ('number', 'null')
                   THEN 0
               WHEN jsonb_typeof(r.inherent_score -> 'severity') = 'number'
                   THEN trunc((r.inherent_score ->> 'severity')::numeric)
               WHEN jsonb_typeof(r.inherent_score -> 'likelihood') = 'number'
                AND jsonb_typeof(r.inherent_score -> 'impact') = 'number'
                   THEN trunc((r.inherent_score ->> 'likelihood')::numeric) * trunc((r.inherent_score ->> 'impact')::numeric)
               ELSE 0
           END AS assessed
) sev ON true
WHERE r.tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg('status')::text IS NULL OR r.status::text = sqlc.narg('status')::text)
  AND (sqlc.arg(include_rollups)::bool OR kids.child_count = 0)
  AND (sqlc.narg('treatment')::text IS NULL OR r.treatment::text = sqlc.narg('treatment')::text)
  AND (sqlc.narg('methodology')::text IS NULL OR r.methodology::text = sqlc.narg('methodology')::text)
  AND (sqlc.narg('level')::text IS NULL OR r.level::text = sqlc.narg('level')::text)
  AND (sqlc.narg('theme')::text IS NULL OR sqlc.narg('theme')::text = ANY(r.themes))
  AND (sqlc.narg('org_unit_id')::uuid IS NULL OR r.org_unit_id = sqlc.narg('org_unit_id')::uuid)
  AND (sqlc.narg('category_ids')::uuid[] IS NULL OR r.category_id = ANY(sqlc.narg('category_ids')::uuid[]))
  AND (sqlc.narg('owner_user_id')::uuid IS NULL OR r.owner_user_id = sqlc.narg('owner_user_id')::uuid)
  AND (NOT sqlc.arg(unowned)::bool OR r.owner_user_id IS NULL)
  AND (sqlc.narg('due_from')::timestamptz IS NULL OR r.review_due_at >= sqlc.narg('due_from')::timestamptz)
  AND (sqlc.narg('due_before')::timestamptz IS NULL OR r.review_due_at < sqlc.narg('due_before')::timestamptz)
  AND (sqlc.narg('severity_min')::int IS NULL OR (r.methodology <> 'fair' AND sev.assessed >= sqlc.narg('severity_min')::int))
  AND (sqlc.narg('severity_max')::int IS NULL OR sev.assessed <= sqlc.narg('severity_max')::int);
