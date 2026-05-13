-- Slice 025 — auditor assignments + audit notes.
--
-- All queries are tenant-scoped via the (tenant_id, ...) prefix; RLS is the
-- defense-in-depth layer. The author-only-read guarantee is enforced at the
-- query layer (`AND author_user_id = $...`) so an auditor cannot read
-- another auditor's notes within the same tenant.

-- ===== auditor_assignments =====

-- name: AssignAuditor :exec
-- Insert an (tenant, user, period) assignment. ON CONFLICT DO NOTHING makes
-- repeated calls idempotent -- granting the same auditor to the same period
-- twice is a no-op rather than an error.
INSERT INTO auditor_assignments (
    tenant_id, user_id, audit_period_id, granted_by, granted_at
)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (tenant_id, user_id, audit_period_id) DO NOTHING;

-- name: ListAuditorAssignmentsForUser :many
-- Returns every assignment the user holds in the current tenant, joined with
-- the period metadata so the /v1/me/audit-period(s) endpoint can render the
-- full picture in one round trip.
SELECT
    ap.id                  AS audit_period_id,
    ap.tenant_id           AS tenant_id,
    ap.name                AS name,
    ap.framework_version_id AS framework_version_id,
    ap.period_start        AS period_start,
    ap.period_end          AS period_end,
    ap.status              AS status,
    ap.frozen_at           AS frozen_at,
    ap.created_at          AS period_created_at,
    aa.granted_at          AS granted_at,
    aa.granted_by          AS granted_by
FROM auditor_assignments aa
JOIN audit_periods ap
  ON ap.tenant_id = aa.tenant_id
 AND ap.id        = aa.audit_period_id
WHERE aa.tenant_id = $1
  AND aa.user_id   = $2
ORDER BY ap.period_start DESC, ap.id ASC;

-- name: GetAuditPeriodIDsForUser :many
-- AttrsResolver hot path -- runs once per auditor request from the OPA
-- middleware. Returns just the period UUIDs so the resolver can stuff
-- them into input.user.attrs.audit_period_ids without joining anything.
SELECT audit_period_id
FROM auditor_assignments
WHERE tenant_id = $1
  AND user_id   = $2
ORDER BY audit_period_id;

-- ===== audit_notes =====

-- name: CreateAuditNote :one
INSERT INTO audit_notes (
    id, tenant_id, audit_period_id, author_user_id,
    scope_type, scope_id, body, visibility, created_at, updated_at
)
VALUES (
    $1, $2, $3, $4,
    $5, $6, $7, 'auditor_only', now(), now()
)
RETURNING *;

-- name: GetAuditNoteByID :one
-- Author-scoped get. A cross-author lookup (auditor B trying to read
-- auditor A's note) returns zero rows -> ErrNotFound at the application
-- layer. This is the second-auditor-cannot-read guarantee (P0-2 / AC-4).
SELECT * FROM audit_notes
WHERE tenant_id = $1
  AND id        = $2
  AND author_user_id = $3;

-- name: ListAuditNotesForAuthorAndPeriod :many
-- Author-scoped list. Same author-only guarantee as GetAuditNoteByID --
-- the WHERE clause pins author_user_id so cross-author reads return
-- empty.
SELECT * FROM audit_notes
WHERE tenant_id        = $1
  AND audit_period_id  = $2
  AND author_user_id   = $3
ORDER BY created_at DESC, id ASC;
