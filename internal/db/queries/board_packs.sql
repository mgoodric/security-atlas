-- Slice 032: quarterly board pack queries.
--
-- The quarterly board pack (canvas §7.5) has a DRAFT -> PUBLISHED lifecycle
-- on a SINGLE row with a stable id (decision D1). These queries are: one
-- INSERT (draft generation), two reads (fetch by id, list), one content
-- UPDATE guarded to draft packs, one publish UPDATE that flips the status,
-- and the board-pack-owned failing-evaluations read that feeds the open
-- findings section (decision D4).
--
-- Tenant scoping: `board_packs` and `control_evaluations` are tenant-scoped —
-- every query runs inside the request's `app.current_tenant` GUC set by
-- tenancy.Middleware; RLS does the filtering and the `tenant_id = $1` WHERE
-- clause is the primary correctness guarantee (canvas invariant #6). Every
-- timestamp cutoff is computed in Go and passed as an explicit parameter —
-- never a single-placeholder expression that would trip pgx type inference
-- (SQLSTATE 42P08).

-- name: InsertBoardPack :one
-- Append one freshly generated DRAFT pack. The pack is created in `draft`
-- status with NULL publish metadata (the status-coherence CHECK enforces
-- that). A re-generation for the same period_end is a NEW row with a NEW id
-- — never an edit of an existing pack.
INSERT INTO board_packs (id, tenant_id, period_end, status, content, narrative_md)
VALUES ($1, $2, $3, 'draft', $4, $5)
RETURNING *;

-- name: GetBoardPackByID :one
-- Fetch one pack by id. RLS scopes the lookup to the caller's tenant — a
-- cross-tenant id returns ErrNoRows (the handler maps that to 404). Works
-- for both draft and published packs.
SELECT * FROM board_packs
WHERE tenant_id = $1 AND id = $2;

-- name: ListBoardPacks :many
-- Enumerate every pack for the tenant, newest report-date first.
SELECT * FROM board_packs
WHERE tenant_id = $1
ORDER BY period_end DESC, created_at DESC, id ASC;

-- name: UpdateBoardPackContent :one
-- Mutate a DRAFT pack's content + re-rendered narrative in place. The
-- `status = 'draft'` predicate is belt-and-suspenders behind the
-- tenant_update RLS policy (which already gates on status = 'draft') and
-- the BEFORE UPDATE trigger: an UPDATE targeting a published pack matches
-- zero rows here and returns pgx.ErrNoRows, which the handler maps to 409.
-- `updated_at` is bumped to now() so the row reflects the last edit.
UPDATE board_packs
SET content = $3,
    narrative_md = $4,
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = 'draft'
RETURNING *;

-- name: PublishBoardPack :one
-- Flip a DRAFT pack to PUBLISHED, stamping the publish metadata. The
-- `status = 'draft'` predicate makes this idempotent-safe: a second publish
-- of an already-published pack matches zero rows and returns pgx.ErrNoRows
-- (the handler maps that to 409). The frozen content + narrative are passed
-- explicitly so the publish writes the final, operator-reviewed snapshot in
-- the same UPDATE that flips the status — one atomic transition.
UPDATE board_packs
SET status = 'published',
    content = $3,
    narrative_md = $4,
    published_by = $5,
    published_at = $6,
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = 'draft'
RETURNING *;

-- name: ListFailingEvaluationsForPack :many
-- Open-findings source for the quarterly board pack (decision D4). The
-- latest evaluation per (control, scope_cell) whose result is 'fail',
-- bounded by the pack's `period_end` horizon. DISTINCT ON collapses the
-- append-only control_evaluations history to the current row per cell as
-- of $2 (the pack's period_end), then the outer filter keeps only the
-- failing ones.
--
-- This is BOARD-PACK-OWNED — deliberately NOT the slice-030
-- `ListFailingEvaluationsAsOf` in oscal_export.sql — because the board pack
-- is a calendar-quarter artifact pinned to `period_end`, NOT an
-- AuditPeriod-bound export pinned to a frozen_at horizon. Same data
-- semantics as slice 030 (a failing control evaluation IS a finding for
-- v1), but a distinct, conflict-free query surface.
SELECT * FROM (
    SELECT DISTINCT ON (control_id, scope_cell_id)
        id, tenant_id, control_id, scope_cell_id, eval_run_id,
        evaluated_at, result, freshness_status, last_observed_at
    FROM control_evaluations
    WHERE tenant_id = $1
      AND evaluated_at <= $2
    ORDER BY control_id, scope_cell_id, evaluated_at DESC, created_at DESC
) latest
WHERE latest.result = 'fail'
ORDER BY latest.control_id ASC, latest.scope_cell_id ASC;
