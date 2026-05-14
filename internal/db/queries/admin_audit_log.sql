-- Slice 062 — admin /v1/admin/audit-log query.
--
-- One query against the admin_audit_log_v view (migration _022). Filters
-- are optional and apply on the view's uniform columns. Pagination uses
-- (ts, source_table, resource_id) as a stable composite cursor — the ts
-- index on each source table covers the ORDER BY without an external
-- sort merge.
--
-- RLS-aware: the view inherits each source table's tenant_read policy,
-- so this SELECT under the tenancy-applied transaction returns only the
-- caller's tenant rows. No tenant_id filter is needed in the WHERE
-- clause; including one would be defense-in-depth but also redundant.
-- We include `tenant_id = $1` explicitly so a future RLS misconfiguration
-- still filters by tenant — the query is correct under both contexts.

-- name: ListAdminAuditLog :many
-- Paginated, filtered union read. NULL filters are treated as "match all"
-- via the standard COALESCE-with-sentinel pattern. The cursor is
-- (cursor_ts, cursor_source_table, cursor_resource_id); rows are returned
-- in (ts DESC, source_table ASC, resource_id ASC) order so the cursor's
-- next-page condition is a tuple comparison.
SELECT
    tenant_id,
    ts,
    source_table,
    event_type,
    actor,
    resource_type,
    resource_id,
    summary
FROM admin_audit_log_v
WHERE tenant_id = $1
  AND (sqlc.arg('actor_filter')::text   = '' OR actor      = sqlc.arg('actor_filter')::text)
  AND (sqlc.arg('event_filter')::text   = '' OR event_type = sqlc.arg('event_filter')::text)
  AND (sqlc.arg('since')::timestamptz IS NULL OR ts >= sqlc.arg('since')::timestamptz)
  AND (sqlc.arg('until')::timestamptz IS NULL OR ts <  sqlc.arg('until')::timestamptz)
  -- Cursor condition: ts strictly less than the cursor's ts, OR same ts
  -- but a strictly greater (source_table, resource_id) tuple.
  AND (sqlc.arg('cursor_ts')::timestamptz IS NULL
       OR ts < sqlc.arg('cursor_ts')::timestamptz
       OR (ts = sqlc.arg('cursor_ts')::timestamptz
           AND (source_table, resource_id) > (sqlc.arg('cursor_source')::text, sqlc.arg('cursor_resource_id')::text)))
ORDER BY ts DESC, source_table ASC, resource_id ASC
LIMIT $2;
