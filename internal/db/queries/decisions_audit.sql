-- Decision Log audit log (slice 055, migration _030).
--
-- decisions_audit is an append-only mutation log: every PATCH, supersede,
-- link add/remove, denied cross-tenant link attempt, and overdue-notification
-- emission writes one row. There is no UPDATE or DELETE query -- the table
-- has no UPDATE/DELETE RLS policy and atlas_app has no UPDATE/DELETE grant,
-- so an UPDATE/DELETE query would fail at runtime anyway. Append-only by
-- construction.
--
-- All queries are tenant-scoped via the leading (tenant_id, ...); RLS under
-- FORCE keeps the cross-tenant boundary safe even on a misconfigured query.

-- name: WriteDecisionAudit :one
-- Append one audit row. `action` is one of the decisions_audit_action_chk
-- enum values; `detail` is free-form context (a diff, a link target id, a
-- replacement decision id, a notification recipient, etc.).
INSERT INTO decisions_audit (
    id, tenant_id, decision_id, action, actor, detail
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListDecisionAudit :many
-- The audit trail for a single decision, oldest first. Powers the
-- decision-detail audit-log rail.
SELECT *
FROM decisions_audit
WHERE tenant_id = $1 AND decision_id = $2
ORDER BY occurred_at ASC, id ASC;

-- name: CountDecisionOverdueNotifications :one
-- Slice 055 overdue-job dedup probe: has this decision already had an
-- `overdue_notified` audit row written? A non-zero count means the daily
-- job already notified the decision_maker -- skip re-emission (P0
-- anti-criterion: one notification per overdue decision, never repeated).
-- Served index-only by idx_decisions_audit_overdue_notified.
SELECT count(*) AS notified_count
FROM decisions_audit
WHERE tenant_id = $1
  AND decision_id = $2
  AND action = 'overdue_notified';
