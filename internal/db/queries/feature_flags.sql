-- Slice 059 — per-tenant feature flags + capability toggles queries.
--
-- All queries are tenant-scoped via the (tenant_id, ...) prefix; RLS is the
-- defense-in-depth layer and the WHERE clauses are the primary correctness
-- guarantee. The flag_key is application-validated (snake_case namespaced)
-- before reaching the DB; the schema only enforces non-empty.
--
-- Every toggle through SetFeatureFlag is paired with one WriteFeatureFlagAuditLog
-- call in the application -- the audit-log table has SELECT + INSERT policies
-- ONLY under FORCE RLS so there is no UPDATE / DELETE path. The pairing is
-- application-enforced via a single transaction in the Store.

-- name: GetFeatureFlag :one
-- Returns the row for (tenant, flag_key). pgx.ErrNoRows when absent; the
-- application falls back to the seed default in that case.
SELECT *
FROM feature_flags
WHERE tenant_id = $1 AND flag_key = $2;

-- name: ListFeatureFlags :many
-- Returns every flag row for the tenant, ordered by flag_key. The
-- application merges this against the seed defaults so the response
-- shape includes never-toggled flags too.
SELECT *
FROM feature_flags
WHERE tenant_id = $1
ORDER BY flag_key ASC;

-- name: UpsertFeatureFlag :one
-- Idempotent toggle write. INSERT-on-first-toggle or UPDATE on subsequent
-- toggles. created_at is set on insert only (excluded from the conflict
-- update clause). The application supplies last_changed_by + last_changed_at
-- so the audit-log row written in the same tx carries matching values.
INSERT INTO feature_flags (
    tenant_id, flag_key, enabled, description, category,
    last_changed_by, last_changed_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (tenant_id, flag_key) DO UPDATE
SET enabled         = EXCLUDED.enabled,
    description     = EXCLUDED.description,
    category        = EXCLUDED.category,
    last_changed_by = EXCLUDED.last_changed_by,
    last_changed_at = EXCLUDED.last_changed_at
RETURNING *;

-- name: WriteFeatureFlagAuditLog :one
-- Append-only. Every toggle writes one row. The audit-log table is
-- append-only by construction (SELECT + INSERT RLS only under FORCE).
INSERT INTO feature_flag_audit_log (
    id, tenant_id, flag_key, from_enabled, to_enabled, actor, reason
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListFeatureFlagAuditLog :many
-- AC-10 read accessor. Returns every audit-log row for the tenant, newest
-- first. The application paginates in-memory when needed (audit-log
-- cardinality is small -- 12 flags, low toggle frequency).
SELECT *
FROM feature_flag_audit_log
WHERE tenant_id = $1
ORDER BY occurred_at DESC, id ASC;

-- name: ListFeatureFlagAuditLogForKey :many
-- Per-flag audit history. Powers the "who toggled this and when" view.
SELECT *
FROM feature_flag_audit_log
WHERE tenant_id = $1 AND flag_key = $2
ORDER BY occurred_at DESC, id ASC;
