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
-- Slice 025 legacy entry point. Pins visibility to 'auditor_only' and
-- parent_note_id to NULL so existing slice-025 callers keep their
-- private-write semantics with zero behavior change. Slice 029 adds the
-- richer CreateAuditNoteV2 below.
INSERT INTO audit_notes (
    id, tenant_id, audit_period_id, author_user_id,
    scope_type, scope_id, body, visibility, parent_note_id, created_at, updated_at
)
VALUES (
    $1, $2, $3, $4,
    $5, $6, $7, 'auditor_only', NULL, now(), now()
)
RETURNING *;

-- name: CreateAuditNoteV2 :one
-- Slice 029 entry point. Accepts visibility ('auditor_only'|'shared') and
-- optional parent_note_id (for replies). The application layer validates
-- the parent's scope+period match before calling; this query trusts the
-- caller for that check.
--
-- OSCAL preservation note (slice 030 contract): every field on this row
-- maps to an OSCAL assessment-results `observation` annotation:
--   - id            -> observation.uuid
--   - body          -> observation.description
--   - author_user_id-> observation.collected
--   - scope_type/id -> observation.subject (object-reference)
--   - parent_note_id-> observation.related-observations
--   - visibility    -> observation.props (custom prop ns="security-atlas")
-- Slice 030 reads via ListThreadForScope; do not change the column set
-- without updating slice 030's mapper.
INSERT INTO audit_notes (
    id, tenant_id, audit_period_id, author_user_id,
    scope_type, scope_id, body, visibility, parent_note_id, created_at, updated_at
)
VALUES (
    $1, $2, $3, $4,
    $5, $6, $7, $8, $9, now(), now()
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

-- name: GetAuditNoteByIDForReader :one
-- Slice 029 visibility-aware get. Returns the row when:
--   - shared visibility (any tenant member can read), OR
--   - author_user_id = caller (private note belonging to caller)
-- Cross-author auditor_only rows return zero rows -> ErrNotFound.
SELECT * FROM audit_notes
WHERE tenant_id = $1
  AND id        = $2
  AND (visibility = 'shared' OR author_user_id = $3);

-- name: ListAuditNotesForAuthorAndPeriod :many
-- Author-scoped list. Same author-only guarantee as GetAuditNoteByID --
-- the WHERE clause pins author_user_id so cross-author reads return
-- empty.
SELECT * FROM audit_notes
WHERE tenant_id        = $1
  AND audit_period_id  = $2
  AND author_user_id   = $3
ORDER BY created_at DESC, id ASC;

-- name: ListThreadForScope :many
-- Slice 029 thread retrieval. Returns every note in the scope's thread
-- visible to the caller (visibility filter applies row-by-row):
--   - 'shared' notes: visible to all tenant members
--   - 'auditor_only' notes: visible only to the author
--
-- A recursive CTE walks from the root note(s) down through replies. The
-- depth guard caps recursion at 100 to prevent runaway traversal on a
-- pathological dataset.
--
-- Parameters:
--   $1 tenant_id (uuid)
--   $2 audit_period_id (uuid)
--   $3 scope_type (text)
--   $4 scope_id (text, NULL-equivalent passed as empty string '')
--   $5 caller user_id (text, for visibility filter)
--
-- The handler MUST pass an empty string for scope_id when the caller
-- meant "no scope_id" -- the WHERE clause coalesces NULL scope_id to
-- '' so the comparison matches. This avoids pgx single-placeholder
-- type-inference issues (SQLSTATE 42P08).
WITH RECURSIVE thread AS (
    -- Root notes for the scope (parent_note_id IS NULL).
    SELECT
        n.id, n.tenant_id, n.audit_period_id, n.author_user_id,
        n.scope_type, n.scope_id, n.body, n.visibility,
        n.parent_note_id, n.created_at, n.updated_at,
        0 AS depth,
        ARRAY[n.created_at] AS sort_path
    FROM audit_notes n
    WHERE n.tenant_id        = $1
      AND n.audit_period_id  = $2
      AND n.scope_type       = $3
      AND COALESCE(n.scope_id, '') = $4::text
      AND n.parent_note_id IS NULL
    UNION ALL
    -- Recursive case: children of any included note.
    SELECT
        c.id, c.tenant_id, c.audit_period_id, c.author_user_id,
        c.scope_type, c.scope_id, c.body, c.visibility,
        c.parent_note_id, c.created_at, c.updated_at,
        t.depth + 1,
        t.sort_path || ARRAY[c.created_at]
    FROM audit_notes c
    JOIN thread t ON c.parent_note_id = t.id
    WHERE c.tenant_id = $1
      AND t.depth < 100
)
SELECT
    id, tenant_id, audit_period_id, author_user_id,
    scope_type, scope_id, body, visibility,
    parent_note_id, created_at, updated_at,
    depth
FROM thread
WHERE visibility = 'shared' OR author_user_id = $5::text
ORDER BY sort_path ASC, id ASC;

-- name: ListThreadAuthorsForScope :many
-- Slice 029 notification dispatch helper. Returns the distinct authors
-- of every note in the thread (shared OR private, all variants), used
-- to compute who should receive a notification when a new reply lands.
-- The notification dispatch layer filters out the new note's own
-- author to avoid self-notification.
--
-- Parameters mirror ListThreadForScope: scope_id is passed as text
-- (empty string for "no scope_id"). COALESCE matches NULL to ''.
SELECT DISTINCT n.author_user_id
FROM audit_notes n
WHERE n.tenant_id        = $1
  AND n.audit_period_id  = $2
  AND n.scope_type       = $3
  AND COALESCE(n.scope_id, '') = $4::text;
