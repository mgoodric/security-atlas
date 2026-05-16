# Slice 104 — decisions log

`GET /v1/anchors?include=state` extension for the `/controls` list view.

## Decisions made

### D1 — Read state from the `control_evaluations` ledger directly (one query), not via `eval.Engine.ControlState`

AC-3 reads: "resolves the state column via the same `eval.Engine.ControlState` path as `GET /v1/controls/{id}/state` — no parallel evaluation path." AC-4 reads: "single CTE / join query — NOT a per-anchor loop calling the engine."

Those two ACs interpret "same evaluation path" two different ways. The Engine method is per-control: walking it 1,400 times is exactly the anti-pattern AC-4 forbids. The narrative is explicit: "the slice exists specifically to avoid the per-row fan-out."

Resolution: "same evaluation path" = same **data source** (the append-only `control_evaluations` ledger, which the engine is the sole writer of, constitutional invariant #2). The new sqlc query reads the same table the engine writes; we do not introduce a parallel evaluation calculation. The aggregation logic (worst-state-wins per anchor) is a SQL `CASE` over the result enum — no probability math, no freshness recomputation. Confidence: high.

### D2 — Worst-state-wins aggregation in SQL

AC-6 says: "When multiple controls satisfy one anchor, … the joined state aggregates per the canvas §6 rollup rule — slice 098 only needs a single representative row, so the 'worst-state cell' wins (fail > insufficient_evidence > pass > not_applicable)."

The DB enum is `evidence_result` with values `pass | fail | na | inconclusive` (slice 002 + slice 012). The slice text's "insufficient_evidence" is a slice-text abstraction over `inconclusive` (the actual DB value). I render the DB value verbatim (no translation) so the wire shape matches `stateWire.result` already in use by `GET /v1/controls/{id}/state` and consumed by `web/lib/api.ts` `ControlStateEntry.result`. The aggregation priority becomes:

`fail` (4) > `inconclusive` (3) > `pass` (2) > `na` (1)

— implemented as `MAX()` over the int ranking. Confidence: high.

Freshness aggregates with the same priority: `expired` > `stale` > `no_evidence` > `fresh`. The slice 098 design doc binds `freshness_status` to the same column as `stateWire.freshness_status`, so I keep the existing vocabulary (`fresh | stale | no_evidence` — slice 012 state.go). `expired` is not currently produced by the engine; reserved in the rank function for forward-compat. Confidence: medium (the slice doesn't pin a freshness rollup rule; I'm extending canvas §6 by analogy).

### D3 — `last_observed_at` aggregation = MAX (most recent any control saw evidence)

When an anchor has two controls each with their own last_observed_at, the joined cell shows the freshest of the two. Rationale: the column communicates "when did evidence under this anchor most recently arrive"; minimum would lie about the most-stale child's stagnation; the worst-state already accounts for staleness via freshness_status. Confidence: high.

### D4 — Latest evaluation per (control, scope_cell), then worst per anchor

The query uses `DISTINCT ON (tenant_id, control_id, scope_cell_id) … ORDER BY … evaluated_at DESC` to pick the latest evaluation per cell (mirrors `ListLatestControlEvaluations` in `control_evaluations.sql`). The outer aggregation then collapses to one row per anchor via the worst-state ranking. This preserves slice 012's append-only ledger semantics — we never lose history, we just pick the current state. Confidence: high.

### D5 — `?include=state` is the only currently-supported `include` value; anything else is ignored silently

Following the slice 037 / slice 094 pattern: unknown query params are not errors. The omitted case (current behaviour, no state column) is the additive-default. Confidence: high.

### D6 — Apply `?include=state` to both the listAnchors paths (latest + version-scoped)

The slice text only names "list" once; the existing handler has two paths (the default current-SCF list + `?framework_version_id=` narrowing). I plumb `include=state` through BOTH so the response shape stays consistent regardless of how the caller narrows. Slice 104's spillover-candidate comment in the prompt mentions a possible `/v1/anchors/{id}` extension; I leave that for a follow-up slice — the slice file pins this to the list endpoint only (AC-1). Confidence: high.

### D7 — No new migration / no new index

`control_evaluations` already has `(tenant_id, control_id, scope_cell_id, evaluated_at DESC)` covered by the `latest_per_cell` index (slice 012). `controls.scf_anchor_id` has the slice-009 partial index `WHERE scf_anchor_id IS NOT NULL`. The join `scf_anchors LEFT JOIN controls ON scf_anchor_id LEFT JOIN latest_eval USING (control_id)` plans cleanly without additional indexes. Confidence: high.

### D8 — RLS path: read scf_anchors with `tenant_id IS NULL` (global catalog), join to tenant-scoped controls + control_evaluations under the tenant GUC

The handler runs in the auth + tenancy middleware chain (slice 033). `scf_anchors` is a global catalog (tenant_id IS NULL); `controls` and `control_evaluations` are tenant-scoped under FORCE ROW LEVEL SECURITY. A LEFT JOIN from anchors → controls → control_evaluations means: anchors that exist (globally) but have no tenant-instantiated control show `state: null`; anchors with controls show the joined evaluation. The tenant GUC is set by `tenancymw`; we do NOT set it in this handler. Confidence: high.

### D9 — Frontend: drop the placeholder, narrow `state` typing to the new wire shape from the BFF

`web/app/(authed)/controls/filters.ts` already declares `AnchorRowState` with `result | freshness_status | last_observed_at`. The page maps `anchorsQ.data.anchors` into `AnchorRow`; today it sets `state: null` unconditionally. After 104 the BFF returns the joined shape — the mapping becomes `state: a.state ?? null`. Confidence: high.

## Revisit once in use

1. Per-framework state on the `framework` filter pill — today the pill is a no-op (filters.ts comment). Lifting that needs the anchor row to carry the per-framework `state` set, which is a real query change (intersect with `fw_to_scf_edges` and `framework_scopes`). Spillover candidate when SOC 2 / ISO 27001 list-view filtering becomes a customer ask.
2. The `expired` freshness value never fires today — the engine's vocabulary is `fresh | stale | no_evidence`. If slice 099+ introduces an "expired" status, the rank function here already places it at the top.
3. The single-anchor `GET /v1/anchors/{id}?include=state` extension is NOT shipped here — slice file pins this to the list endpoint. File a spillover if a single-anchor caller needs it.
4. Performance: with ~1,400 anchors and a typical 50-control tenant, the join returns ~1,400 rows total. The query plan is single-shot; no fan-out. We don't add an EXPLAIN ANALYZE harness in this slice — re-measure when a tenant breaches ~5,000 controls.

## Confidence per decision

| Decision                                                          | Confidence |
| ----------------------------------------------------------------- | ---------- |
| D1 — Read directly from control_evaluations (one query)           | high       |
| D2 — Worst-state-wins via MAX over enum rank                      | high       |
| D3 — last_observed_at via MAX                                     | high       |
| D4 — DISTINCT ON for latest-per-cell, then worst-per-anchor       | high       |
| D5 — Unknown include values silently ignored                      | high       |
| D6 — `?include=state` applies to both list paths                  | high       |
| D7 — No new index needed                                          | high       |
| D8 — Global scf_anchors + tenant-scoped controls/evals join shape | high       |
| D9 — Frontend narrows state typing from BFF shape                 | high       |
