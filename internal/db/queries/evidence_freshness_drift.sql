-- Slice 016 — evidence freshness read-model + control drift snapshot queries.
--
-- `evidence_freshness` is a materialized current-state read model (one row per
-- (tenant_id, control_id), UPSERTed on refresh). `control_drift_snapshots` is
-- an append-only daily snapshot ledger (every refresh APPENDS; latest row per
-- (tenant_id, snapshot_date) wins). Both are DERIVED purely from the immutable
-- ledgers (`evidence_records`, `control_evaluations`) — slice 016 never writes
-- the ledgers (constitutional invariant #2).
--
-- All queries are tenant-scoped via the (tenant_id, ...) prefix; RLS is the
-- defense-in-depth layer and the WHERE clauses are the primary correctness
-- guarantee (canvas invariant #6). Every timestamp / date cutoff is computed
-- in Go and passed as an explicit parameter — never a single-placeholder
-- expression that would trip pgx type inference (SQLSTATE 42P08).

-- ===== evidence_freshness =====

-- name: ListControlsWithLatestEvidence :many
-- The freshness refresh input: every active (non-superseded) control for the
-- tenant, joined to its evidence ledger aggregate (freshest observed_at +
-- record count). LEFT JOIN so a control with zero evidence still produces a
-- row (latest_observed_at NULL, evidence_count 0). The evidence join matches
-- BOTH the UUID control_id path and the free-form control_ref path (slice
-- 013), so evidence pushed under an SCF-anchor string is still counted.
SELECT
    c.id                              AS control_id,
    c.freshness_class                 AS freshness_class,
    max(e.observed_at)::timestamptz   AS latest_observed_at,
    count(e.id)                       AS evidence_count
FROM controls c
LEFT JOIN evidence_records e
    ON e.tenant_id = c.tenant_id
   AND (e.control_id = c.id OR e.control_ref = c.id::text)
WHERE c.tenant_id = $1
  AND c.superseded_by IS NULL
GROUP BY c.id, c.freshness_class;

-- name: UpsertEvidenceFreshness :one
-- The freshness refresh write: UPSERT one control's freshness row onto the
-- (tenant_id, control_id) unique key. valid_until and is_stale are computed
-- in Go (Go owns the canvas §2.3 class -> max-age mapping via
-- internal/eval.FreshnessMaxAge — never re-derived in SQL). On conflict every
-- derived column is refreshed.
INSERT INTO evidence_freshness (
    id, tenant_id, control_id, freshness_class,
    latest_observed_at, valid_until, is_stale, evidence_count, refreshed_at
) VALUES (
    $1, $2, $3, $4,
    $5, $6, $7, $8, $9
)
ON CONFLICT (tenant_id, control_id) DO UPDATE SET
    freshness_class    = EXCLUDED.freshness_class,
    latest_observed_at = EXCLUDED.latest_observed_at,
    valid_until        = EXCLUDED.valid_until,
    is_stale           = EXCLUDED.is_stale,
    evidence_count     = EXCLUDED.evidence_count,
    refreshed_at       = EXCLUDED.refreshed_at
RETURNING *;

-- name: ListEvidenceFreshness :many
-- AC-1 / AC-2 read: every freshness row for the tenant. The handler buckets
-- by freshness_class and counts is_stale in Go — a flat list keeps the query
-- single-purpose and lets the handler shape both the ?bucket=class
-- distribution and the per-class stale counts from one read.
SELECT *
FROM evidence_freshness
WHERE tenant_id = $1
ORDER BY freshness_class NULLS LAST, control_id;

-- name: GetEvidenceFreshnessByControl :one
-- Single-control freshness lookup — used by tests and by future per-control
-- detail surfaces.
SELECT *
FROM evidence_freshness
WHERE tenant_id = $1
  AND control_id = $2;

-- ===== control_drift_snapshots =====

-- name: ListPassingControlsForDay :many
-- The drift snapshot input: for one tenant, the set of controls "passing" as
-- of `as_of` under the worst-cell rollup. A control passes iff EVERY
-- applicable (control, scope_cell) tuple's LATEST evaluation at or before
-- `as_of` has result='pass' AND freshness_status='fresh'. Stale evidence does
-- NOT count as passing (canvas §2.3: stale evidence drives the drift signal).
--
-- The inner DISTINCT ON collapses the append-only control_evaluations ledger
-- to the latest row per (control_id, scope_cell_id). The outer aggregate
-- keeps a control only when NO cell is non-(pass+fresh): bool_and over the
-- per-cell pass+fresh predicate.
WITH latest_per_cell AS (
    SELECT DISTINCT ON (control_id, scope_cell_id)
        control_id,
        scope_cell_id,
        result,
        freshness_status
    FROM control_evaluations
    WHERE tenant_id = $1
      AND evaluated_at <= $2
    ORDER BY control_id, scope_cell_id, evaluated_at DESC, created_at DESC
)
SELECT control_id
FROM latest_per_cell
GROUP BY control_id
HAVING bool_and(result = 'pass' AND freshness_status = 'fresh');

-- name: InsertDriftSnapshot :one
-- The drift refresh write: APPEND one snapshot row. The table is append-only
-- (no UPDATE/DELETE RLS policy) — every refresh, scheduled or on-ingest,
-- appends a fresh row; the read path takes the latest row per
-- (tenant_id, snapshot_date). controls_passing and passing_control_ids are
-- both computed in Go from ListPassingControlsForDay and MUST agree (CHECK
-- constraint control_drift_snapshots_count_matches_set).
INSERT INTO control_drift_snapshots (
    id, tenant_id, snapshot_date,
    controls_passing, passing_control_ids, trigger
) VALUES (
    $1, $2, $3,
    $4, $5, $6
)
RETURNING *;

-- name: ListLatestDriftSnapshotsSince :many
-- AC-3 read: the latest snapshot per calendar day for the tenant, for every
-- day from `since_date` through today. DISTINCT ON (snapshot_date) collapses
-- the append-only intra-day refresh trail to the authoritative latest row per
-- day. The handler diffs consecutive days' passing_control_ids to derive the
-- signed delta and the flipped-to-fail control set. `since_date` is computed
-- in Go (today - N days) and passed explicitly.
SELECT DISTINCT ON (snapshot_date) *
FROM control_drift_snapshots
WHERE tenant_id = $1
  AND snapshot_date >= $2
ORDER BY snapshot_date DESC, captured_at DESC;

-- name: GetLatestDriftSnapshotForDay :one
-- The latest snapshot for one specific calendar day — used by the on-ingest
-- refresh to decide whether today already has a snapshot, and by tests.
SELECT *
FROM control_drift_snapshots
WHERE tenant_id = $1
  AND snapshot_date = $2
ORDER BY captured_at DESC
LIMIT 1;
