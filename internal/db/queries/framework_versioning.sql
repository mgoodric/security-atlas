-- slice 484: framework-versioning capability — the lifecycle + migration-suggest
-- review queue + audit queries (ADR 0019). All targets are CATALOG tables (no
-- tenant RLS); the trust gate is admin-role authz + the append-only audit.

-- ===== version lifecycle =====

-- name: GetFrameworkVersionByID :one
-- Plain read of one framework_version. Returns ErrNoRows for an unknown id.
SELECT * FROM framework_versions WHERE id = $1;

-- name: GetFrameworkVersionByIDForUpdate :one
-- Row-lock the version inside the promotion transaction so a concurrent
-- promote/revert cannot race the read-validate-write window.
SELECT * FROM framework_versions WHERE id = $1 FOR UPDATE;

-- name: GetCurrentFrameworkVersion :one
-- The framework's current (status='current') global-catalog version, if any.
-- Returns ErrNoRows when no current version exists yet.
SELECT * FROM framework_versions
WHERE framework_id = $1 AND status = 'current' AND tenant_id IS NULL;

-- name: SetFrameworkVersionStatus :exec
-- Flip ONLY the status of one version (the narrow column-level UPDATE grant —
-- slice 484 D2). Legality is enforced in Go (internal/frameworkversion) BEFORE
-- this runs; this is the unconditional write inside the lifecycle tx.
UPDATE framework_versions
SET status = $2
WHERE id = $1;

-- name: GetFrameworkByID :one
SELECT * FROM frameworks WHERE id = $1;

-- ===== migration-suggest review queue =====

-- name: ListFrameworkVersionRequirementCodes :many
-- All requirement codes for a version, used by the suggest engine to compute
-- the exact-code set-intersection / set-difference between two versions.
SELECT id, code FROM framework_requirements
WHERE framework_version_id = $1
ORDER BY code;

-- name: InsertFrameworkVersionMigration :one
-- Append one suggested/flagged carryover row to the review queue. The job
-- writes ONLY 'pending' rows; it never mutates a requirement or edge
-- (P0-484-1 / AC-3). Idempotent re-runs are absorbed by the UNIQUE
-- (from_version_id, to_version_id, requirement_code, match_kind) constraint —
-- ON CONFLICT DO NOTHING leaves a previously-decided row untouched.
INSERT INTO framework_version_migrations (
    framework_id, from_version_id, to_version_id,
    from_requirement_id, to_requirement_id,
    requirement_code, match_kind
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (from_version_id, to_version_id, requirement_code, match_kind)
DO NOTHING
RETURNING *;

-- name: ListFrameworkVersionMigrations :many
-- The review queue for one version pair, oldest first. Admin-scoped read.
SELECT * FROM framework_version_migrations
WHERE from_version_id = $1 AND to_version_id = $2
ORDER BY match_kind, requirement_code;

-- name: GetFrameworkVersionMigrationForUpdate :one
-- Row-lock one queue entry inside the approve/reject tx. Returns ErrNoRows for
-- an unknown id.
SELECT * FROM framework_version_migrations WHERE id = $1 FOR UPDATE;

-- name: SetFrameworkVersionMigrationDecision :one
-- Record a human's approve/reject on one queue row. Only a 'pending' row can
-- be decided (the WHERE status='pending' guard makes a double-decide a no-op
-- that returns no row — the store maps that to ErrAlreadyDecided).
UPDATE framework_version_migrations
SET status      = $2,
    reviewer_id = $3,
    note        = $4,
    decided_at  = now()
WHERE id = $1 AND status = 'pending'
RETURNING *;

-- ===== audit (append-only) =====

-- name: InsertFrameworkVersionAudit :one
-- One immutable audit row per lifecycle transition or migration decision
-- (threat-model R / AC-1 / AC-4). Written in the SAME tx as the act.
INSERT INTO framework_version_audit (
    framework_id, framework_version_id, migration_id,
    action, from_status, to_status, actor_id, note
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListFrameworkVersionAudit :many
-- Audit history for one framework (newest first). Admin-scoped.
SELECT * FROM framework_version_audit
WHERE framework_id = $1
ORDER BY created_at DESC;
