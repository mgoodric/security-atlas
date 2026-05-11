-- name: CreateScopeDimension :one
-- Insert a per-tenant scope dimension declaration. Used by the bootstrap seed
-- (is_builtin=true) and by admins adding custom dimensions (is_builtin=false).
-- tenant_id is captured directly so RLS evaluates the policy on insert.
INSERT INTO scope_dimensions (id, tenant_id, name, value_type, allowed_values, is_required, is_builtin)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetScopeDimensionByName :one
-- Resolve a dimension by name within the current tenant context.
SELECT *
FROM scope_dimensions
WHERE tenant_id = $1 AND name = $2;

-- name: ListScopeDimensions :many
-- Enumerate the active tenant's declared dimensions. Ordering: builtins first
-- (stable presentation in the admin UI), then alphabetical by name.
SELECT *
FROM scope_dimensions
WHERE tenant_id = $1
ORDER BY is_builtin DESC, name ASC;

-- name: CreateScopeCell :one
-- Insert a scope cell. dimensions_hash is the application-computed canonical
-- hash; the UNIQUE (tenant_id, dimensions_hash) constraint rejects duplicates.
INSERT INTO scope_cells (id, tenant_id, label, dimensions, dimensions_hash)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListScopeCells :many
-- Enumerate the active tenant's scope cells. Newest first.
SELECT *
FROM scope_cells
WHERE tenant_id = $1
ORDER BY created_at DESC, id ASC;

-- name: GetScopeCellByID :one
SELECT *
FROM scope_cells
WHERE tenant_id = $1 AND id = $2;

-- name: GetScopeCellByHash :one
-- Look up a cell by its dimensions hash. Used by the "create or get" path so
-- a re-seed call does not 409 on the existing default cell.
SELECT *
FROM scope_cells
WHERE tenant_id = $1 AND dimensions_hash = $2;

-- name: CountScopeCells :one
SELECT COUNT(*)::bigint
FROM scope_cells
WHERE tenant_id = $1;

-- name: GetControlApplicabilityExpr :one
-- Returns the JSON-encoded applicability_expr for a single control. The column
-- is TEXT (slice 002); slice 017 stores JSON in that text.
SELECT id, applicability_expr
FROM controls
WHERE tenant_id = $1 AND id = $2;
