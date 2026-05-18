-- Slice 126 — audit_sink_failures queries.
--
-- The fallback ledger for the external audit-log sink. Writes happen
-- from the sink writer goroutine (internal/audit/sink) when either
-- the bounded channel rejects an Emit OR the file-side write fails.
-- Reads happen from ops investigation paths + the slice 126 integration
-- test asserting AC-8 backpressure semantics.
--
-- Both queries are tenant-scoped via RLS — the caller MUST have applied
-- the tenant context on the underlying tx (via tenancy.ApplyTenant)
-- before invoking either. No tenant_id parameter is passed across the
-- API boundary (defense-in-depth + parity with slice 124).

-- name: WriteSinkFailure :one
-- Append one row to the failure ledger. failure_reason is one of the
-- two CHECK enum values ('buffer_overflow' | 'write_error');
-- error_text is the OS error string for write_error, empty for
-- buffer_overflow.
INSERT INTO audit_sink_failures (
    tenant_id,
    failure_reason,
    entry_kind,
    entry_actor,
    entry_target_type,
    entry_target_id,
    entry_action,
    error_text
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING id, tenant_id, occurred_at, failure_reason, entry_kind,
          entry_actor, entry_target_type, entry_target_id, entry_action,
          error_text;

-- name: CountSinkFailures :one
-- Return the row count for the caller's tenant. Used by the slice 126
-- integration test to assert exactly-1 fallback row after the 10001-record
-- backpressure scenario.
SELECT COUNT(*) FROM audit_sink_failures;

-- name: ListRecentSinkFailures :many
-- The last N failures for the caller's tenant, newest first. Used by
-- ops paths (a future admin UI surface) and by the integration test
-- asserting projected-Entry fields landed correctly.
SELECT id, tenant_id, occurred_at, failure_reason, entry_kind,
       entry_actor, entry_target_type, entry_target_id, entry_action,
       error_text
FROM audit_sink_failures
ORDER BY occurred_at DESC, id DESC
LIMIT $1;
