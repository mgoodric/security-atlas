-- name: CreateDecision :one
-- Insert a new Decision Log entry (canvas §6.7). Slice 052 ships the table
-- + queries; slice 055 adds the HTTP CRUD surface. decision_id is the
-- tenant-visible identifier ("DL-2026-04-12"); the application generates it.
INSERT INTO decisions (
    id, tenant_id, decision_id, title, narrative, constraints,
    tradeoffs, decision_maker, decided_at, revisit_by, status, superseded_by
)
VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11, $12
)
RETURNING *;

-- name: GetDecisionByID :one
SELECT *
FROM decisions
WHERE tenant_id = $1 AND id = $2;

-- name: GetDecisionByDecisionID :one
-- Lookup by the human-readable decision_id ("DL-2026-04-12"). Unique within
-- tenant (UNIQUE (tenant_id, decision_id)).
SELECT *
FROM decisions
WHERE tenant_id = $1 AND decision_id = $2;

-- name: ListDecisions :many
SELECT *
FROM decisions
WHERE tenant_id = $1
ORDER BY decided_at DESC, id ASC;

-- name: ListDecisionsByStatus :many
SELECT *
FROM decisions
WHERE tenant_id = $1 AND status = $2
ORDER BY decided_at DESC, id ASC;

-- name: ListDecisionsDueForRevisit :many
-- Decisions with revisit_by ≤ the cutoff. Slice 055's dashboard panel uses
-- this to surface "decisions due to revisit in the next N days".
SELECT *
FROM decisions
WHERE tenant_id = $1
  AND status = 'active'
  AND revisit_by IS NOT NULL
  AND revisit_by <= $2
ORDER BY revisit_by ASC, id ASC;

-- name: UpdateDecision :one
UPDATE decisions
SET title = $3,
    narrative = $4,
    constraints = $5,
    tradeoffs = $6,
    decision_maker = $7,
    decided_at = $8,
    revisit_by = $9,
    status = $10,
    superseded_by = $11,
    updated_at = now()
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: DeleteDecision :exec
DELETE FROM decisions
WHERE tenant_id = $1 AND id = $2;

-- name: CountDecisionsByDecidedDate :one
-- Slice 055: count decisions whose decided_at falls on a given UTC calendar
-- date. Drives the per-tenant per-day NNNN sequence in the
-- DL-YYYY-MM-DD-NNNN identifier. $2 is the start-of-day (inclusive), $3 the
-- start of the next day (exclusive).
SELECT count(*) AS same_day_count
FROM decisions
WHERE tenant_id = $1
  AND decided_at >= $2
  AND decided_at < $3;

-- name: SupersedeDecision :one
-- Slice 055: mark a decision superseded and point superseded_by at the
-- replacement. The WHERE status = 'active' guard means a non-active (already
-- superseded / expired) decision returns zero rows -- the store
-- disambiguates that into a 409. The old row is never deleted (P0
-- anti-criterion); only its status + superseded_by + updated_at change.
UPDATE decisions
SET status = 'superseded',
    superseded_by = $3,
    updated_at = now()
WHERE tenant_id = $1 AND id = $2 AND status = 'active'
RETURNING *;

-- name: SetDecisionAuditNarrativeOptOut :one
-- Slice 055: flip the per-decision OSCAL-narrative opt-out flag.
UPDATE decisions
SET audit_narrative_opt_out = $3,
    updated_at = now()
WHERE tenant_id = $1 AND id = $2
RETURNING *;

-- name: ListOverdueDecisions :many
-- Slice 055: active decisions whose revisit_by has already passed. $2 is
-- "today" (a DATE). Powers GET /v1/decisions/overdue and the daily
-- overdue-notification job.
SELECT *
FROM decisions
WHERE tenant_id = $1
  AND status = 'active'
  AND revisit_by IS NOT NULL
  AND revisit_by < $2
ORDER BY revisit_by ASC, id ASC;

-- name: ListTenantsWithOverdueDecisions :many
-- Slice 055: every tenant with at least one active, overdue decision. Run
-- by the daily overdue-notification job as the migrator role (BYPASSRLS)
-- to enumerate tenants before applying each tenant's GUC. $1 is "today".
SELECT DISTINCT tenant_id
FROM decisions
WHERE status = 'active'
  AND revisit_by IS NOT NULL
  AND revisit_by < $1;
