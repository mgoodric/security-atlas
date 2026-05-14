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
SELECT id, tenant_id, control_id, control_ref, scope_id,
       observed_at, evidence_kind, provenance, hash
FROM evidence_records
WHERE tenant_id = $1
  AND (control_id = $2 OR control_ref = $3)
  AND observed_at >= $4
  AND observed_at <= $5
  AND (
        observed_at < sqlc.arg(cursor_ts)
        OR (observed_at = sqlc.arg(cursor_ts) AND id < sqlc.arg(cursor_id))
      )
ORDER BY observed_at DESC, id DESC
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
SELECT r.id, r.title, r.inherent_score, r.residual_score,
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
       freshness_status, evidence_count_in_window
FROM control_evaluations
WHERE tenant_id = $1
  AND control_id = $2
  AND (
        evaluated_at < sqlc.arg(cursor_ts)
        OR (evaluated_at = sqlc.arg(cursor_ts) AND id < sqlc.arg(cursor_id))
      )
ORDER BY evaluated_at DESC, id DESC
LIMIT sqlc.arg(row_limit);
