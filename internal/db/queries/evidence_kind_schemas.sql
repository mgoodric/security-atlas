-- name: ListEvidenceKindSchemasGlobal :many
-- Returns every globally-registered schema (tenant_id IS NULL). The HTTP
-- list endpoint pages with limit/offset; ordering by (kind, major DESC,
-- minor DESC, patch DESC) puts the newest versions first within each kind.
SELECT *
FROM evidence_kind_schemas
WHERE tenant_id IS NULL
ORDER BY kind, major DESC, minor DESC, patch DESC
LIMIT $1 OFFSET $2;

-- name: ListEvidenceKindSchemasForTenant :many
-- Returns every schema visible to the current tenant — both global rows
-- (tenant_id IS NULL) and the tenant's private rows. RLS already gates
-- tenant rows, but the WHERE clause is explicit so the query is the same
-- shape with or without RLS in effect.
SELECT *
FROM evidence_kind_schemas
WHERE tenant_id IS NULL OR tenant_id = $1
ORDER BY kind, major DESC, minor DESC, patch DESC
LIMIT $2 OFFSET $3;

-- name: GetEvidenceKindSchemaGlobal :one
-- Look up one global schema by (kind, semver). Used by the GET endpoint
-- when no tenant is supplied and by the push validator before slice 013
-- lands tenant-private validation.
SELECT *
FROM evidence_kind_schemas
WHERE tenant_id IS NULL AND kind = $1 AND semver = $2;

-- name: GetEvidenceKindSchemaForTenant :one
-- Look up one schema visible to the tenant. Prefers the tenant's private
-- row when one exists for the same (kind, semver); otherwise returns the
-- global row. The ORDER BY puts the non-null tenant_id first.
SELECT *
FROM evidence_kind_schemas
WHERE (tenant_id IS NULL OR tenant_id = $1)
  AND kind = $2
  AND semver = $3
ORDER BY tenant_id NULLS LAST
LIMIT 1;

-- name: ListEvidenceKindSchemaVersionsForKind :many
-- Returns every registered semver for a kind, visible to the tenant.
-- Slice 014 uses this for AC-5 semver enforcement (a POST must check the
-- prior versions of the same kind before accepting a new one).
SELECT *
FROM evidence_kind_schemas
WHERE (tenant_id IS NULL OR tenant_id = $1)
  AND kind = $2
ORDER BY major DESC, minor DESC, patch DESC;

-- name: InsertEvidenceKindSchema :one
-- Insert a new schema row. The caller (slice 014 registry service) parses
-- semver into major/minor/patch and supplies all three; the DB CHECK
-- prevents negatives. Owner must be non-empty (CHECK constraint). The
-- partial unique indexes on (kind, semver) WHERE tenant_id IS NULL and
-- (tenant_id, kind, semver) WHERE tenant_id IS NOT NULL enforce
-- duplicate-version rejection.
INSERT INTO evidence_kind_schemas (
    id, tenant_id, kind, semver, major, minor, patch,
    schema_json, owner, default_scf_anchors, created_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: ListAllEvidenceKindSchemas :many
-- Bypass-RLS path used at boot by the platform-schema importer running as
-- atlas_migrate. Returns every row regardless of tenant. Never reachable
-- through the app role under RLS.
SELECT * FROM evidence_kind_schemas;
