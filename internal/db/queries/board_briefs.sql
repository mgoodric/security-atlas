-- Slice 031: monthly board brief pinned-snapshot queries.
--
-- The board brief is an append-only frozen snapshot (canvas §7.5). These
-- queries are: one INSERT (generation), two reads (fetch by id, list), and
-- the read-model inputs the Generator assembles a brief FROM — registered
-- frameworks and a date-bounded top-risks read.
--
-- Tenant scoping: `board_briefs` and `risks` are tenant-scoped — every query
-- that touches them runs inside the request's `app.current_tenant` GUC set
-- by tenancy.Middleware; RLS does the filtering. The `frameworks` catalog
-- read intentionally has no tenant filter beyond the tenant_id predicate
-- below (frameworks may be global `tenant_id IS NULL` or tenant-private).

-- name: InsertBoardBrief :one
-- Append one generated brief. The table is append-only (slice 031 migration
-- `_031`): there is no UPDATE/DELETE path. A re-generation for the same
-- period_end is a NEW row with a NEW id — never an edit (AC anti-criterion).
INSERT INTO board_briefs (id, tenant_id, period_end, generated_at, content, narrative_md)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetBoardBriefByID :one
-- Fetch one frozen brief by id. RLS scopes the lookup to the caller's
-- tenant — a cross-tenant id returns ErrNoRows (the handler maps that to
-- 404). The returned `content` + `narrative_md` are the verbatim frozen
-- snapshot — AC-5 (re-fetch returns the original content).
SELECT * FROM board_briefs
WHERE tenant_id = $1 AND id = $2;

-- name: ListBoardBriefs :many
-- Enumerate every brief for the tenant, newest report-date first.
SELECT * FROM board_briefs
WHERE tenant_id = $1
ORDER BY period_end DESC, generated_at DESC, id ASC;

-- name: ListFrameworksForTenant :many
-- The registered frameworks the program runs against — both the global
-- catalog (`tenant_id IS NULL`) and any tenant-private frameworks. Drives
-- the per-framework posture rows in the brief (one row per framework).
SELECT * FROM frameworks
WHERE tenant_id IS NULL OR tenant_id = $1
ORDER BY slug;

-- name: ListRisksForBoardBrief :many
-- Date-bounded top-risks read for the board brief's "top-3 risks aging"
-- section. Board-package-owned (NOT the shared risks.sql ListRisks) so the
-- parallel slice 066 — which extends ListRisks — stays conflict-free.
--
-- Returns every risk whose treatment is still open (NOT 'accept' with a
-- still-valid acceptance is the operator's call; here we include all risks
-- and let the Generator rank by residual severity then age in Go — the
-- residual_score JSONB shape is methodology-dependent, so the numeric
-- extraction is done in Go, not a JSONB-path SQL expression that would trip
-- pgx type inference). `created_at` lower bound is passed explicitly so pgx
-- never has to infer a bare-placeholder type (SQLSTATE 42P08).
SELECT * FROM risks
WHERE tenant_id = $1
  AND created_at <= $2
ORDER BY updated_at ASC, id ASC;

-- name: BoardBriefExceptionAggregate :one
-- Slice 751 — the DETERMINISTIC exception-status aggregate the board brief
-- freezes into `content.exceptions` and the AI-drafted exception-status
-- narrative section grounds every numeric claim on.
--
-- Board-package-owned (NOT in exceptions.sql) for the same reason
-- ListRisksForBoardBrief is: the board brief's read of another module's table
-- is a projection the board owns, so a change to the exception module's own
-- query set never silently changes what the board reports.
--
-- Three numbers, and only three (decisions log D1):
--   active_count             exceptions in force right now (status='active').
--   past_due_count           of those, the ones already past their own
--                            expires_at — a waiver outliving its sunset date.
--   oldest_active_started_at when the longest-standing active waiver began
--                            applying. Returned as a TIMESTAMP, not a day
--                            count: the age arithmetic is done in Go against
--                            the brief's generated_at so it uses the same
--                            clock (and the same overridable test clock) as
--                            the risk-aging path, rather than the DB's now().
--                            NULL when nothing is active.
--
-- Tenant scoping: the explicit tenant_id predicate is the primary filter and
-- the slice-021 FORCE ROW LEVEL SECURITY policy on `exceptions` is the
-- defense-in-depth layer — another tenant's waiver is invisible to the
-- aggregate, so it can never inflate a board-facing count (invariant #6).
-- The COALESCE start chain mirrors the lifecycle: effective_from is set at
-- activation, approved_at at approval, requested_at always.
SELECT
    COUNT(*) FILTER (WHERE status = 'active')::bigint AS active_count,
    COUNT(*) FILTER (
        WHERE status = 'active' AND expires_at <= sqlc.arg(as_of)::timestamptz
    )::bigint AS past_due_count,
    (
        MIN(COALESCE(effective_from, approved_at, requested_at))
            FILTER (WHERE status = 'active')
    )::timestamptz AS oldest_active_started_at
FROM exceptions
WHERE tenant_id = sqlc.arg(tenant_id)::uuid;
