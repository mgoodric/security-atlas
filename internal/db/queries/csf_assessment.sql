-- Slice 515 — NIST CSF 2.0 Tier / Profile assessment queries.
--
-- Conventions match slice 018 / 512:
--   * tenant_id is always the first parameter on tenant-scoped reads even
--     though RLS reads it from the app.current_tenant GUC — the explicit
--     predicate keeps query plans on the (tenant_id, …) indexes.
--   * Time stamps (now()) are issued by the DB so the application doesn't
--     choose a clock.
--   * The gap-view read joins selections to the SHARED CSF Subcategory rows
--     (framework_requirements) by FK — the per-Subcategory↔SCF-anchor mapping
--     is NEVER re-stored here (invariant #1, P0-515-2); the anchor/coverage
--     traversal is done by internal/api/ucfcoverage at read time.

-- ===== csf_tier_ratings =====

-- name: UpsertCsfTierRating :one
-- Insert or update the single Tier rating per (tenant, framework_version).
-- Re-rating updates the row in place; the caller appends a csf_assessment_audit
-- row ('tier_rated' on insert, 'tier_rerated' on the conflict path) and reads
-- the returned `(xmax = 0)` flag to know which it was.
INSERT INTO csf_tier_ratings (
    id, tenant_id, framework_version_id, tier, rationale, rated_by
)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (tenant_id, framework_version_id) DO UPDATE
SET tier       = EXCLUDED.tier,
    rationale  = EXCLUDED.rationale,
    rated_by   = EXCLUDED.rated_by,
    rated_at   = now(),
    updated_at = now()
RETURNING *, (xmax = 0) AS inserted;

-- name: GetCsfTierRating :one
SELECT *
FROM csf_tier_ratings
WHERE tenant_id = $1 AND framework_version_id = $2;

-- ===== csf_profiles =====

-- name: UpsertCsfProfile :one
-- Insert (or no-op update) the single profile per (tenant, framework_version,
-- kind). Re-creating the same kind returns the existing row (its name/updated
-- refreshed) rather than erroring, so the editor is idempotent.
INSERT INTO csf_profiles (
    id, tenant_id, framework_version_id, kind, name, created_by
)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (tenant_id, framework_version_id, kind) DO UPDATE
SET name       = EXCLUDED.name,
    updated_at = now()
RETURNING *, (xmax = 0) AS inserted;

-- name: GetCsfProfile :one
SELECT *
FROM csf_profiles
WHERE tenant_id = $1 AND framework_version_id = $2 AND kind = $3;

-- name: GetCsfProfileByID :one
SELECT *
FROM csf_profiles
WHERE tenant_id = $1 AND id = $2;

-- ===== csf_profile_selections =====

-- name: UpsertCsfProfileSelection :one
-- Set the target outcome for one Subcategory inside a profile. Re-setting the
-- same (profile, Subcategory) updates the outcome + note in place.
INSERT INTO csf_profile_selections (
    id, tenant_id, csf_profile_id, framework_requirement_id, target_outcome, note
)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (csf_profile_id, framework_requirement_id) DO UPDATE
SET target_outcome = EXCLUDED.target_outcome,
    note           = EXCLUDED.note,
    updated_at     = now()
RETURNING *, (xmax = 0) AS inserted;

-- name: DeleteCsfProfileSelection :one
-- Clear a Subcategory selection (the operator removed the target outcome).
-- Returns the deleted row so the caller can audit what was cleared.
DELETE FROM csf_profile_selections
WHERE tenant_id = $1 AND csf_profile_id = $2 AND framework_requirement_id = $3
RETURNING *;

-- name: ListCsfProfileSelectionsWithSubcategory :many
-- The gap-view row source for ONE profile: every selection joined to its
-- shared CSF Subcategory (code + title) so the handler can render the
-- per-Subcategory target outcome without a second round trip. Newest CSF
-- Subcategory ordering is by code (e.g. GV.OC-01, GV.OC-02, …).
SELECT
    s.id,
    s.csf_profile_id,
    s.framework_requirement_id,
    s.target_outcome,
    s.note,
    fr.code  AS subcategory_code,
    fr.title AS subcategory_title
FROM csf_profile_selections s
JOIN framework_requirements fr ON fr.id = s.framework_requirement_id
WHERE s.tenant_id = $1 AND s.csf_profile_id = $2
ORDER BY fr.code;

-- ===== csf_assessment_audit =====

-- name: InsertCsfAssessmentAudit :one
INSERT INTO csf_assessment_audit (
    id, tenant_id, framework_version_id, subject_kind, subject_id, action, actor, detail
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListCsfAssessmentAudit :many
-- Newest first. Exposed for the audit trail + integration assertions.
SELECT *
FROM csf_assessment_audit
WHERE tenant_id = $1 AND framework_version_id = $2
ORDER BY occurred_at DESC, id ASC;
