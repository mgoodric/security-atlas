-- Slice 468: server-backed control owner-assignment + saved filter-views.
--
-- Every query here is tenant-scoped on tenant_id ($1 or sqlc.arg) AND runs
-- inside a tenant-GUC tx — RLS is the real boundary; the explicit tenant_id
-- predicate keeps the query plan tight and is belt-and-braces. The single-
-- item assign path and the bulk path BOTH go through UpsertControlOwner so
-- the per-item write is byte-identical (no drift — P0-467-1 / AC-11).

-- name: GetControlOwnerAssignment :one
-- The current owner-user assignment for a control (if any). Returns
-- pgx.ErrNoRows when the control has no assigned owner-user yet.
SELECT tenant_id, control_id, owner_user_id, assigned_by, assigned_at, updated_at
FROM control_owner_assignments
WHERE tenant_id = $1 AND control_id = $2;

-- name: UpsertControlOwner :one
-- Assign (or re-assign) a control's owner-user. The SINGLE per-item write
-- the single-item path AND the bulk path both call — one row per control.
-- Re-assigning UPSERTs onto the (tenant_id, control_id) PK.
INSERT INTO control_owner_assignments (
    tenant_id, control_id, owner_user_id, assigned_by, assigned_at, updated_at
) VALUES ($1, $2, $3, $4, now(), now())
ON CONFLICT (tenant_id, control_id) DO UPDATE
    SET owner_user_id = EXCLUDED.owner_user_id,
        assigned_by   = EXCLUDED.assigned_by,
        updated_at    = now()
RETURNING tenant_id, control_id, owner_user_id, assigned_by, assigned_at, updated_at;

-- name: ControlExistsInTenant :one
-- Per-item existence + tenant check (AC-6 / AC-7): does this control id
-- exist and is it visible to the calling tenant? Run inside the tenant-GUC
-- tx so RLS hides cross-tenant rows — a control in another tenant returns
-- false. The bulk path calls this per submitted id before any mutation.
SELECT EXISTS (
    SELECT 1 FROM controls
    WHERE tenant_id = $1 AND id = $2 AND superseded_by IS NULL
) AS control_exists;

-- name: UserExistsInTenant :one
-- Target-owner validation (AC-6, threat-model T): is owner_user_id a real,
-- active user in the calling tenant? RLS hides cross-tenant users; the
-- status gate rejects a disabled user as an assignment target.
SELECT EXISTS (
    SELECT 1 FROM users
    WHERE tenant_id = $1 AND id = $2 AND status = 'active'
) AS user_exists;

-- name: InsertOwnerAssignmentAudit :one
-- Append one repudiation-ledger row (threat-model R / P0-448-4). is_bulk
-- distinguishes a bulk event (control_ids carries the whole applied set)
-- from a single-item event (one id). Append-only by RLS construction.
INSERT INTO control_owner_assignment_audit_log (
    tenant_id, actor_user_id, owner_user_id, control_ids, is_bulk
) VALUES ($1, $2, $3, $4, $5)
RETURNING id, occurred_at;

-- name: ListOwnerAssignmentAudit :many
-- Read the assignment audit ledger for the tenant, newest first. Used by
-- the integration tests (and a future "who reassigned these?" surface).
SELECT id, tenant_id, occurred_at, actor_user_id, owner_user_id, control_ids, is_bulk
FROM control_owner_assignment_audit_log
WHERE tenant_id = $1
ORDER BY occurred_at DESC, id DESC
LIMIT $2;
