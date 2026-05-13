-- Slice 028 — AuditPeriod + freezing primitive.
--
-- All queries are tenant-scoped via the (tenant_id, ...) prefix; RLS is the
-- defense-in-depth layer. The freeze path is guarded by `status='open'` so
-- a re-freeze UPDATE matches zero rows (anti-criterion P0: no retroactive
-- mutation of frozen periods).

-- name: CreateAuditPeriod :one
-- Insert a period with status='open'. frozen_at / frozen_hash / frozen_by
-- are NULL on create (enforced by audit_periods_frozen_coherent CHECK).
INSERT INTO audit_periods (
    id, tenant_id, name, framework_version_id,
    period_start, period_end, status,
    frozen_at, frozen_hash, frozen_by,
    created_by, created_at, updated_at
)
VALUES (
    $1, $2, $3, $4, $5, $6,
    'open', NULL, NULL, NULL,
    $7, now(), now()
)
RETURNING *;

-- name: GetAuditPeriodByID :one
SELECT * FROM audit_periods
WHERE tenant_id = $1 AND id = $2;

-- name: ListAuditPeriodsByTenant :many
SELECT * FROM audit_periods
WHERE tenant_id = $1
ORDER BY created_at DESC, id ASC;

-- name: FreezeAuditPeriod :one
-- AC-2 + AC-6: flip status open->frozen, stamp the freeze metadata. The
-- `WHERE status='open'` guard means re-freezing a frozen row matches zero
-- rows (RETURNING returns nothing) and the application surfaces 409
-- Conflict. The CHECK constraint audit_periods_frozen_coherent enforces
-- that the freeze tuple (frozen_at, frozen_hash, frozen_by) is set together.
UPDATE audit_periods
SET status      = 'frozen',
    frozen_at   = $3,
    frozen_hash = $4,
    frozen_by   = $5,
    updated_at  = now()
WHERE tenant_id = $1
  AND id        = $2
  AND status    = 'open'
RETURNING *;

-- name: WriteAuditPeriodLog :one
-- Append-only lifecycle log. action is constrained at the DB layer
-- (period_created | period_frozen | freeze_rejected_already_frozen |
-- population_attached). detail is a free-form JSONB for action-specific
-- payload (e.g., the rejected freeze attempt records the offending actor).
INSERT INTO audit_period_audit_log (
    id, tenant_id, audit_period_id, action, actor, detail, occurred_at
)
VALUES ($1, $2, $3, $4, $5, $6, now())
RETURNING *;

-- name: ListAuditPeriodLog :many
-- Used by the audit-trail re-audit flow and by the integration test
-- (verifies that period_created + period_frozen rows landed).
SELECT *
FROM audit_period_audit_log
WHERE tenant_id = $1 AND audit_period_id = $2
ORDER BY occurred_at DESC, id ASC;

-- name: ListEvidenceIDsForPeriodHash :many
-- Hash-input ingredient #1 (ADR 0003): the sorted UUIDs of evidence records
-- visible at the period's horizon. The frozen-at filter is parameterized so
-- a verifier can replay the hash for any horizon.
-- Returns evidence_records.id only; the order is ASC by id so the SHA-256
-- input is canonical without an extra sort pass in Go.
SELECT id
FROM evidence_records
WHERE tenant_id   = $1
  AND observed_at <= $2
ORDER BY id;

-- name: ListControlIDsForPeriodHash :many
-- Hash-input ingredient #2 (ADR 0003): the sorted UUIDs of controls in the
-- tenant's catalog. v1 takes the full tenant catalog; a future slice may
-- narrow to controls satisfied by the period's framework_version_id once
-- canvas §8 audit-scope-narrowing lands.
SELECT id
FROM controls
WHERE tenant_id = $1
ORDER BY id;

-- name: ListEvidenceForPeriodControl :many
-- AC-3: control-state read for a period. Returns evidence records for one
-- control bounded by the period's frozen_at horizon (or live when the
-- period is still 'open', in which case the caller passes NULL and the
-- COALESCE-to-infinity short-circuits to live state). Ordered by
-- observed_at DESC so the caller can pick the most-recent record as the
-- pass/fail-driving observation.
SELECT id, observed_at, result, hash
FROM evidence_records
WHERE tenant_id  = $1
  AND control_id = $2
  AND observed_at <= COALESCE(sqlc.narg('frozen_at')::timestamptz,
                              'infinity'::timestamptz)
ORDER BY observed_at DESC, id ASC;

-- name: AttachPopulationToPeriod :exec
-- AC-4: stamp populations.audit_period_id with the period id. The
-- populations.frozen_at column gets stamped in a sibling statement
-- (SetPopulationFrozenAt) when the period is frozen at the time of
-- attachment; if the period is still open, frozen_at remains NULL and the
-- slice-026 query path keeps the population live until the period freezes.
UPDATE populations
SET audit_period_id = $3
WHERE tenant_id = $1
  AND id        = $2
  AND audit_period_id IS NULL;

-- name: SetPopulationFrozenAt :exec
-- Stamp populations.frozen_at from the parent period's frozen_at. Called
-- both at attach-time (if the period is already frozen) and at freeze-time
-- (for all populations already attached to this period). The slice-026
-- query path then enforces `observed_at <= populations.frozen_at` on
-- subsequent draws.
UPDATE populations
SET frozen_at = $3
WHERE tenant_id       = $1
  AND audit_period_id = $2
  AND frozen_at IS NULL;
