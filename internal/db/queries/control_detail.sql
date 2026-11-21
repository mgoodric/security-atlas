-- Slice 064 — control-detail backend read endpoints.
--
-- Four pure SELECTs that surface existing data behind per-control read
-- paths for the slice-041 control-detail view. This slice adds NO migration
-- and NO write surface — it reads the evidence ledger, the policy library,
-- risk_control_links + risks, and the control_evaluations ledger.
--
-- All queries are tenant-scoped via the (tenant_id, ...) prefix; RLS is the
-- defense-in-depth layer and the WHERE clauses are the primary correctness
-- guarantee (canvas invariant #6). Every pagination cutoff is computed in Go
-- and passed as an explicit parameter — never a single-placeholder
-- expression that would trip pgx type inference (SQLSTATE 42P08).
--
-- Cursor pagination over the two append-only ledgers (evidence_records,
-- control_evaluations) is keyset, not OFFSET: a (timestamp, id) keyset is
-- stable under concurrent appends, which OFFSET is not. The handler passes
-- the decoded cursor's timestamp + id; a zero/sentinel cursor selects the
-- first page. Each query fetches limit+1 rows so the handler can tell
-- whether a next page exists without a second COUNT round-trip.

-- name: ListEvidenceForControlPaged :many
-- AC-1: paginated evidence-ledger records resolved for one control, bounded
-- by the [since, until] observed_at window. Resolution reuses slice 012's
-- control->evidence path verbatim: (control_id = $2 OR control_ref = $3),
-- where $3 is the UUID's string form (slice 012's loadEvidence passes
-- controlRef := ctrlID.String()). The keyset predicate is decomposed
-- (not a row-comparison tuple) so sqlc infers each placeholder's type
-- correctly — a (observed_at, id) ROW(...) comparison mis-infers the id
-- placeholder as timestamptz under sqlc v1.31. The decomposed form
--   observed_at < cursor_ts OR (observed_at = cursor_ts AND id < cursor_id)
-- is the same keyset semantics: ties on observed_at fall back to id. A
-- sentinel cursor (cursor_ts = 'infinity', cursor_id = max-uuid) selects
-- the first page. The handler computes all cutoff values in Go.
--
-- Slice 106: the SELECT projects `result` so the wire shape `evidenceWire`
-- can surface it (the column has always been on evidence_records; the slice
-- 064 wire shape simply omitted it).
--
-- #2470: id_only drops the control_ref leg, so the rows are exactly the
-- control_id = $2 set the auditor hydrator (audit.Store.ControlPeriods) and
-- the population draw resolve. The handler sets it for a period-scoped
-- caller; every other caller keeps the slice-012 OR resolution.
-- #2817: plus the tenant's unbound records of a kind in unbound_kinds that
-- the control's evidence_queries declare (internal/evidence/unbound).
SELECT id, tenant_id, control_id, control_ref, scope_id,
       observed_at, evidence_kind, provenance, hash, result,
       source_attribution
FROM evidence_records
WHERE evidence_records.tenant_id = $1
  AND (control_id = $2 OR (NOT sqlc.arg(id_only)::boolean AND control_ref = $3)
       OR (control_id IS NULL
           AND evidence_kind = ANY(sqlc.arg(unbound_kinds)::text[])
           AND EXISTS (SELECT 1 FROM controls uc
                       WHERE uc.tenant_id = evidence_records.tenant_id AND uc.id = $2
                         AND uc.evidence_queries @> jsonb_build_array(
                               jsonb_build_object('evidence_kind', evidence_records.evidence_kind)))))
  AND observed_at >= $4
  AND observed_at <= $5
  AND (
        observed_at < sqlc.arg(cursor_ts)
        OR (observed_at = sqlc.arg(cursor_ts) AND evidence_records.id < sqlc.arg(cursor_id))
      )
ORDER BY observed_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: CountEvidenceForControlWindow :one
-- #2470: the `total` of GET /v1/evidence?control_id=. Counts the rows
-- ListEvidenceForControlPaged pages through, before the cursor: the same
-- control resolution (id_only included) and [since, until] window. The
-- per-control list ignores scope_cell_id, so the count does too.
SELECT count(*)
FROM evidence_records
WHERE evidence_records.tenant_id = $1
  AND (control_id = $2 OR (NOT sqlc.arg(id_only)::boolean AND control_ref = $3)
       OR (control_id IS NULL
           AND evidence_kind = ANY(sqlc.arg(unbound_kinds)::text[])
           AND EXISTS (SELECT 1 FROM controls uc
                       WHERE uc.tenant_id = evidence_records.tenant_id AND uc.id = $2
                         AND uc.evidence_queries @> jsonb_build_array(
                               jsonb_build_object('evidence_kind', evidence_records.evidence_kind)))))
  AND observed_at >= $4
  AND observed_at <= $5;

-- name: ListEvidencePaged :many
-- Slice 106: tenant-wide evidence ledger window with optional filters. Used
-- when GET /v1/evidence is called WITHOUT a control_id — returns the
-- caller-tenant's whole evidence stream (RLS still scopes it on top of the
-- explicit tenant_id predicate, defense-in-depth per canvas invariant #6).
--
-- All five filter args are optional. The
--     (sqlc.narg('x')::T IS NULL OR col = sqlc.narg('x')::T)
-- pattern is the established NULL-skip shape — see
-- internal/db/queries/vendors.sql:50 — and keeps the query plan stable while
-- letting sqlc emit nullable Go parameters. The source-actor filters drill
-- into the JSONB source_attribution column (slice 013 enforces the
-- {actor_type, actor_id, session_id} shape there).
--
-- Slice 234: a fifth optional filter `scope_cell_id` narrows to one
-- scope cell. #2771: a record is in the cell when the scope it was pushed
-- with (`scope_canonical`, [{key, values[]}]) covers every one of the
-- cell's dimensions: for each (k, v) in scope_cells.dimensions, the
-- record has key k with v among its values. That is the cartesian-cell
-- reading scope.MatchRecordScope applies to credentials. jsonb `@>` does
-- the per-element subset test against a pattern built once from the
-- cell (an uncorrelated subquery, so Postgres runs it as an InitPlan).
-- `scope_id` (FK to `scopes`) is NULL on every pushed record and is not
-- consulted. Rows with no scope_canonical (pushed before slice 474)
-- never match. The subquery requires the cell in the caller's tenant: an
-- out-of-tenant or unknown cell id yields NULL, and `@> NULL` matches
-- nothing, never the whole ledger. Cost notes in
-- docs/audit-log/2771-evidence-filter-pills-decisions.md.
--
-- Keyset pagination mirrors ListEvidenceForControlPaged exactly: same
-- decomposed (observed_at, id) predicate, same +1 probe-row idiom.
-- Window default (last 30 days) is still applied by the handler, not the
-- query — the handler passes the resolved cutoff timestamps verbatim.
SELECT er.id, er.tenant_id, er.control_id, er.control_ref, er.scope_id,
       er.observed_at, er.evidence_kind, er.provenance, er.hash, er.result,
       er.source_attribution
FROM evidence_records er
WHERE er.tenant_id = $1
  AND er.observed_at >= $2
  AND er.observed_at <= $3
  AND (sqlc.narg('kind')::text IS NULL
       OR er.evidence_kind = sqlc.narg('kind')::text)
  AND (sqlc.narg('result_filter')::text IS NULL
       OR er.result::text = sqlc.narg('result_filter')::text)
  AND (sqlc.narg('source_actor_type')::text IS NULL
       OR er.source_attribution->>'actor_type' = sqlc.narg('source_actor_type')::text)
  AND (sqlc.narg('source_actor_id')::text IS NULL
       OR er.source_attribution->>'actor_id' = sqlc.narg('source_actor_id')::text)
  AND (sqlc.narg('scope_cell_id')::uuid IS NULL
       OR er.scope_canonical @> (
            SELECT COALESCE(
                     (SELECT jsonb_agg(jsonb_build_object(
                                 'key', d.key,
                                 'values', jsonb_build_array(d.value)))
                      FROM jsonb_each(sc.dimensions) AS d),
                     '[]'::jsonb)
            FROM scope_cells sc
            WHERE sc.id = sqlc.narg('scope_cell_id')::uuid
              AND sc.tenant_id = $1
          ))
  AND (
        er.observed_at < sqlc.arg(cursor_ts)
        OR (er.observed_at = sqlc.arg(cursor_ts) AND er.id < sqlc.arg(cursor_id))
      )
ORDER BY er.observed_at DESC, er.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListPoliciesLinkedToControl :many
-- AC-2: policies linked to one control via slice 022's policies.linked_-
-- control_ids UUID[] array. The array-containment predicate
-- linked_control_ids @> ARRAY[sqlc.arg(control_id)::uuid] resolves the
-- linkage through the existing slice-022 column — no re-derivation. The
-- control id is a single named scalar arg cast to uuid inside the array
-- literal; sqlc then types it as a plain pgtype.UUID (a bare $2 would be
-- typed []pgtype.UUID, which pgx cannot encode as a uuid array element).
-- Newest-first so the rail reads naturally; id ASC tiebreaks for stable
-- ordering. The policy library is small per the canvas v1 scope, so this
-- endpoint is not paginated.
SELECT id, title, version, status
FROM policies
WHERE tenant_id = $1
  AND linked_control_ids @> ARRAY[sqlc.arg(control_id)::uuid]
ORDER BY created_at DESC, id ASC;

-- name: ListRisksLinkedToControl :many
-- AC-3: risks linked to one control via slice 020's risk_control_links.
-- One join from the link table to risks; the per-link design_score is the
-- human-set design-quality factor surfaced as link_weight (decisions log
-- D7). residual_score + inherent_score are the risk's computed/authored
-- JSONB, passed through. The risk register is small per canvas v1 scope,
-- so this endpoint is not paginated. Newest-link-first; risk id ASC
-- tiebreaks.
SELECT r.id, r.title, r.methodology, r.inherent_score, r.residual_score,
       l.design_score, l.created_at
FROM risk_control_links l
JOIN risks r
  ON r.tenant_id = l.tenant_id
 AND r.id = l.risk_id
WHERE l.tenant_id = $1
  AND l.control_id = $2
ORDER BY l.created_at DESC, r.id ASC;

-- name: ListControlEvaluationHistoryPaged :many
-- AC-4: the control's evaluation history from slice 012's control_eval-
-- uations append-only ledger, newest-first, keyset-paginated. The keyset
-- predicate is decomposed (not a ROW(...) comparison) for the same
-- sqlc-type-inference reason as ListEvidenceForControlPaged:
--   evaluated_at < cursor_ts OR (evaluated_at = cursor_ts AND id < cursor_id)
-- ties on evaluated_at fall back to id; a sentinel cursor selects the
-- first page. The handler computes the cutoff values in Go. This is a
-- pure SELECT over the append-only ledger — no evaluation is triggered
-- (constitutional invariant #2).
SELECT id, evaluated_at, scope_cell_id, result,
       freshness_status, evidence_count_in_window,
       trigger, triggered_by
FROM control_evaluations
WHERE tenant_id = $1
  AND control_id = $2
  AND (
        evaluated_at < sqlc.arg(cursor_ts)
        OR (evaluated_at = sqlc.arg(cursor_ts) AND id < sqlc.arg(cursor_id))
      )
ORDER BY evaluated_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: GetControlBundleForDetail :one
-- Issue #2392: the bundle a control row belongs to, for the anchor-history
-- read. No row (pgx.ErrNoRows) for an id that is not this tenant's control,
-- which the handler maps to 404; RLS and the explicit tenant id both bind.
SELECT bundle_id
FROM controls
WHERE tenant_id = $1 AND id = $2;

-- name: ListControlAnchorHistoryPaged :many
-- Issue #2392: GET /v1/controls/{id}/anchor-history. Every SCF remap
-- decision (#1776, scf_remap_decisions) on any revision of the control's
-- bundle, newest first, keyset paged on (decided_at, id) as the evaluation
-- history is. new_version is the version of the superseding revision an
-- approval created; NULL on a reject or keep-legacy. A pure read of an
-- append-only table.
SELECT d.id, d.decision, d.bulk,
       d.legacy_control_id, lc.version AS legacy_version,
       d.legacy_scf_id, d.legacy_release,
       d.new_control_id, nc.version AS new_version,
       d.new_scf_id, d.new_release,
       d.reason, d.decided_by, d.decided_at
FROM scf_remap_decisions d
JOIN controls lc
  ON lc.tenant_id = d.tenant_id AND lc.id = d.legacy_control_id
LEFT JOIN controls nc
  ON nc.tenant_id = d.tenant_id AND nc.id = d.new_control_id
WHERE d.tenant_id = sqlc.arg(tenant_id)
  AND lc.bundle_id = sqlc.arg(bundle_id)
  AND (
        d.decided_at < sqlc.arg(cursor_ts)
        OR (d.decided_at = sqlc.arg(cursor_ts) AND d.id < sqlc.arg(cursor_id))
      )
ORDER BY d.decided_at DESC, d.id DESC
LIMIT sqlc.arg(row_limit);
