-- Slice 021 — exception / waiver workflow queries.
--
-- All queries are tenant-scoped via the (tenant_id, ...) prefix; RLS is the
-- defense-in-depth layer and the WHERE clauses are the primary correctness
-- guarantee. No update path mutates expires_at (anti-criterion P0: no
-- auto-renewal). Every state transition has a paired application call to
-- WriteExceptionAuditLog so the audit trail is complete (anti-criterion P0:
-- no silent expiry).

-- name: CreateException :one
INSERT INTO exceptions (
    id, tenant_id, control_id, scope_cell_predicate,
    justification, compensating_controls,
    requested_by, requested_at,
    expires_at, status
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'requested')
RETURNING *;

-- name: GetExceptionByID :one
SELECT *
FROM exceptions
WHERE tenant_id = $1 AND id = $2;

-- name: ListExceptions :many
-- Returns every exception for the tenant, newest first. The handler applies
-- status filter in-memory because the cardinality is small (canvas §1.4
-- solo lead, ~30-80 vendors gives a sense of scale; exception count is
-- bounded similarly).
SELECT *
FROM exceptions
WHERE tenant_id = $1
ORDER BY created_at DESC, id ASC;

-- name: ListExceptionsByControl :many
-- AC-4 read accessor: every active row for a given control. Downstream
-- evaluation engine (slice 020/012) intersects scope_cell_predicate with
-- the cell under evaluation.
SELECT *
FROM exceptions
WHERE tenant_id = $1
  AND control_id = $2
  AND status = 'active'
ORDER BY expires_at ASC, id ASC;

-- name: ListExpiringExceptions :many
-- AC-6 calendar surface. Returns active exceptions whose expires_at is
-- within the supplied window. Window upper bound is computed in Go from
-- the request param to keep the SQL static.
SELECT *
FROM exceptions
WHERE tenant_id = $1
  AND status = 'active'
  AND expires_at <= $2
ORDER BY expires_at ASC, id ASC;

-- name: ApproveException :one
-- AC-3 transition: requested -> approved. The application MUST verify the
-- caller has IsApprover before invoking this query AND that approved_by
-- differs from requested_by (segregation of duties). The WHERE
-- status='requested' clause guards against double-approval / out-of-order
-- transitions; zero rows returned indicates either a missing row or a wrong
-- prior state -- the application probes after to disambiguate.
UPDATE exceptions
SET status = 'approved',
    approved_by = $3,
    approved_at = now(),
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = 'requested'
RETURNING *;

-- name: DenyException :one
-- AC-3 transition: requested -> denied (terminal). Same guards as Approve.
UPDATE exceptions
SET status = 'denied',
    denied_by = $3,
    denied_at = now(),
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = 'requested'
RETURNING *;

-- name: ActivateException :one
-- AC-4 enable: approved -> active. effective_from is the operator-supplied
-- moment when the waiver effect begins. The DB does not gate on
-- (effective_from > now); slice scope keeps that to the application layer
-- so future slices can introduce scheduled-activation tooling without a
-- schema change.
UPDATE exceptions
SET status = 'active',
    activated_by = $3,
    activated_at = now(),
    effective_from = $4,
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = 'approved'
RETURNING *;

-- name: ExpireActiveExceptionsBefore :many
-- AC-5 auto-expiry: marks active rows whose expires_at < threshold as
-- expired. Returns the affected rows so the cron can write one
-- exception_audit_log row per expired exception (anti-criterion P0: no
-- silent expiry). The handler executes this inside a tenant-tx and pairs
-- each returned row with WriteExceptionAuditLog. Idempotent: a second
-- run on the same threshold finds zero active rows that have already
-- expired, so it is a no-op.
UPDATE exceptions
SET status = 'expired',
    expired_at = now(),
    updated_at = now()
WHERE tenant_id = $1
  AND status = 'active'
  AND expires_at < $2
RETURNING *;

-- name: ListTenantsWithActiveExceptions :many
-- The auto-expiry cron walks tenants that currently hold any active
-- exception. Returns the distinct tenant_ids so the cron can apply each
-- tenant's GUC before running ExpireActiveExceptionsBefore. Runs as the
-- migrator role (BYPASSRLS) so it sees every tenant.
SELECT DISTINCT tenant_id
FROM exceptions
WHERE status = 'active';

-- name: WriteExceptionAuditLog :one
-- Append-only. Every lifecycle transition writes one row (including
-- system-driven auto-expiry). The exception_audit_log table has
-- SELECT+INSERT policies only under FORCE RLS so no UPDATE/DELETE path
-- exists.
--
-- Slice 180: explicit `subject_module='core'` (column defaults to 'core' at
-- the DB layer; explicit-is-clearer per AC-5).
INSERT INTO exception_audit_log (
    id, tenant_id, exception_id,
    action, actor, from_state, to_state, reason, subject_module
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'core')
RETURNING *;

-- name: ListExceptionAuditLog :many
SELECT *
FROM exception_audit_log
WHERE tenant_id = $1 AND exception_id = $2
ORDER BY occurred_at ASC, id ASC;
