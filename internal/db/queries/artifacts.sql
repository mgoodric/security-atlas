-- name: CreateArtifact :one
-- Insert an artifact row after the blob has been written to S3. RLS
-- evaluates the INSERT WITH CHECK against current_tenant. content_hash
-- may be NULL today but every code path in slice 036 supplies it.
INSERT INTO artifacts (
    id, tenant_id, storage_key, content_hash, size_bytes,
    content_type, uploaded_by
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: GetArtifact :one
-- Look up an artifact by id. RLS USING current_tenant_matches(tenant_id)
-- means the row is invisible to other tenants — handler interprets
-- pgx.ErrNoRows as 404 (no existence leak).
SELECT * FROM artifacts
WHERE tenant_id = $1 AND id = $2;

-- name: FindArtifactByHash :one
-- Dedup lookup: returns an existing artifact id when the same content
-- has already been uploaded by this tenant. Partial unique index
-- (tenant_id, content_hash) WHERE content_hash IS NOT NULL keeps the
-- result single-row.
SELECT * FROM artifacts
WHERE tenant_id = $1 AND content_hash = $2;

-- name: LogArtifactAccess :exec
-- Append a row to the access log. Action is enforced by CHECK
-- ('upload' | 'download'). Caller passes tenant_id + artifact_id + actor.
INSERT INTO artifact_access_log (
    id, tenant_id, artifact_id, action, actor
)
VALUES (
    $1, $2, $3, $4, $5
);

-- name: ListArtifactAccessLog :many
-- Per-artifact recent history. Used by the admin view (slice 040) and
-- the audit-export bundler (slice 029). Cap at 100 rows.
SELECT * FROM artifact_access_log
WHERE tenant_id = $1 AND artifact_id = $2
ORDER BY occurred_at DESC
LIMIT 100;
