-- ActionPlan primitive (slice 384). Tenant-scoped CRUD + M2M linkage +
-- append-only audit log. Every query filters tenant_id explicitly (RLS is
-- the backstop; the explicit predicate keeps the plan index-friendly).

-- name: CreateActionPlan :one
INSERT INTO action_plans (
    id, tenant_id, title, description, triggering_event,
    owner_id, due_date, status, audit_period_id
)
VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9
)
RETURNING *;

-- name: GetActionPlanByID :one
-- Live (non-tombstoned) plan by id. A tombstoned plan reads as absent
-- (AC-14: subsequent GET returns 404).
SELECT *
FROM action_plans
WHERE tenant_id = $1 AND id = $2 AND tombstoned_at IS NULL;

-- name: ListActionPlans :many
-- Cursor pagination on created_at DESC, id DESC. The cursor is the
-- (created_at, id) of the last row of the previous page; when the cursor
-- timestamp is NULL (sqlc.narg), the first page is returned. Tombstoned
-- rows are excluded.
SELECT *
FROM action_plans
WHERE tenant_id = $1
  AND tombstoned_at IS NULL
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
  AND (
        sqlc.narg('cursor_created_at')::timestamptz IS NULL
        OR (created_at, id) < (sqlc.narg('cursor_created_at')::timestamptz, sqlc.narg('cursor_id')::uuid)
      )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit');

-- name: ListActionPlansSnapshot :many
-- AC-27 audit-period-freezing snapshot: only plans created on or before the
-- period's frozen_at horizon are included in the period's snapshot. Live
-- state continues independently; this read is the frozen view.
SELECT *
FROM action_plans
WHERE tenant_id = $1
  AND tombstoned_at IS NULL
  AND created_at <= $2
ORDER BY created_at DESC, id DESC;

-- name: UpdateActionPlan :one
-- Updates the editable fields + status in one statement. The DB transition
-- trigger (action_plans_status_transition_trg) rejects an illegal status
-- edge; the store also validates before calling. updated_at is bumped.
UPDATE action_plans
SET title = $3,
    description = $4,
    triggering_event = $5,
    owner_id = $6,
    due_date = $7,
    status = $8,
    audit_period_id = $9,
    updated_at = now()
WHERE tenant_id = $1 AND id = $2 AND tombstoned_at IS NULL
RETURNING *;

-- name: TombstoneActionPlan :one
-- Soft-delete (P0-384-6). Sets tombstoned_at; the row is preserved. A
-- second tombstone is a no-op-miss (already tombstoned -> zero rows -> 404).
UPDATE action_plans
SET tombstoned_at = now(),
    updated_at = now()
WHERE tenant_id = $1 AND id = $2 AND tombstoned_at IS NULL
RETURNING *;

-- ===== M2M: risks =====

-- name: LinkActionPlanRisk :exec
-- Idempotent at the handler layer (it checks existence first); the PK makes
-- a duplicate INSERT a unique-violation the store maps to 409.
INSERT INTO action_plan_risks (action_plan_id, risk_id, tenant_id, linked_by)
VALUES ($1, $2, $3, $4);

-- name: UnlinkActionPlanRisk :execrows
DELETE FROM action_plan_risks
WHERE tenant_id = $1 AND action_plan_id = $2 AND risk_id = $3;

-- name: ListActionPlanRisks :many
SELECT risk_id, linked_at, linked_by
FROM action_plan_risks
WHERE tenant_id = $1 AND action_plan_id = $2
ORDER BY linked_at ASC;

-- name: CountActionPlanRisks :one
SELECT count(*)
FROM action_plan_risks
WHERE tenant_id = $1 AND action_plan_id = $2;

-- name: ActionPlanRiskExists :one
SELECT EXISTS (
    SELECT 1 FROM action_plan_risks
    WHERE tenant_id = $1 AND action_plan_id = $2 AND risk_id = $3
);

-- name: ListActionPlanIDsForRisk :many
-- Powers the "Linked Action Plans" read-only section on /risks/{id} (AC-25).
-- Joins to action_plans so tombstoned plans are excluded.
SELECT ap.id, ap.title, ap.status, ap.due_date
FROM action_plan_risks apr
JOIN action_plans ap ON ap.id = apr.action_plan_id AND ap.tenant_id = apr.tenant_id
WHERE apr.tenant_id = $1 AND apr.risk_id = $2 AND ap.tombstoned_at IS NULL
ORDER BY ap.created_at DESC;

-- ===== M2M: controls =====

-- name: LinkActionPlanControl :exec
INSERT INTO action_plan_controls (action_plan_id, control_id, tenant_id, linked_by)
VALUES ($1, $2, $3, $4);

-- name: UnlinkActionPlanControl :execrows
DELETE FROM action_plan_controls
WHERE tenant_id = $1 AND action_plan_id = $2 AND control_id = $3;

-- name: ListActionPlanControls :many
SELECT control_id, linked_at, linked_by
FROM action_plan_controls
WHERE tenant_id = $1 AND action_plan_id = $2
ORDER BY linked_at ASC;

-- name: CountActionPlanControls :one
SELECT count(*)
FROM action_plan_controls
WHERE tenant_id = $1 AND action_plan_id = $2;

-- name: ActionPlanControlExists :one
SELECT EXISTS (
    SELECT 1 FROM action_plan_controls
    WHERE tenant_id = $1 AND action_plan_id = $2 AND control_id = $3
);

-- name: ListActionPlanIDsForControl :many
-- Powers the "Linked Action Plans" read-only section on /controls/{id} (AC-26).
SELECT ap.id, ap.title, ap.status, ap.due_date
FROM action_plan_controls apc
JOIN action_plans ap ON ap.id = apc.action_plan_id AND ap.tenant_id = apc.tenant_id
WHERE apc.tenant_id = $1 AND apc.control_id = $2 AND ap.tombstoned_at IS NULL
ORDER BY ap.created_at DESC;

-- ===== existence probes for cross-tenant linkage guards =====

-- name: ActionPlanRiskExistsInTenant :one
-- AC-17/AC-29: a risk_id that does not resolve in the caller's tenant is a
-- 404 (cross-tenant deny). RLS hides the cross-tenant row so EXISTS is false.
SELECT EXISTS (
    SELECT 1 FROM risks WHERE tenant_id = $1 AND id = $2
);

-- name: ActionPlanControlExistsInTenant :one
-- AC-19: same cross-tenant deny for controls.
SELECT EXISTS (
    SELECT 1 FROM controls WHERE tenant_id = $1 AND id = $2
);

-- name: ActionPlanOwnerExistsInTenant :one
-- AC-10 tampering guard: owner_id must resolve to a user in the caller's
-- tenant. RLS hides cross-tenant users.
SELECT EXISTS (
    SELECT 1 FROM users WHERE tenant_id = $1 AND id = $2
);

-- ===== append-only audit log =====

-- name: WriteActionPlanAuditLog :one
-- Every mutation writes one row (AC-16). UPDATE/DELETE are rejected by the
-- append-only trigger; this INSERT is the only path that writes here.
INSERT INTO action_plan_audit_log (
    id, tenant_id, action_plan_id, actor_id, action_type, before_state, after_state
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListActionPlanAuditLog :many
SELECT *
FROM action_plan_audit_log
WHERE tenant_id = $1 AND action_plan_id = $2
ORDER BY created_at ASC, id ASC;
