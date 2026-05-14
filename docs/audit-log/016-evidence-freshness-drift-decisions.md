# Slice 016 — Evidence freshness + drift detection — decisions log

**Slice type:** `AFK` (the ACs are mechanically verifiable). This log is not
a JUDGMENT-slice sign-off gate — it records the **canvas-interpretation
judgment calls** the slice surfaced, so post-deployment iteration is
tractable. None of these blocked merge.

## Decisions made

### D1 — The drift definition: what does "a control is passing" mean?

Canvas §7.1 defines *Drift count* as `(controls passing yesterday) −
(controls passing today)`, signed — but "passing" is underspecified across
three axes. Resolved:

- **Grain: per-control, worst-cell rollup.** A control "passes" on a calendar
  day iff EVERY applicable `(control, scope_cell)` tuple's latest evaluation
  that day has `result='pass'` AND `freshness_status='fresh'`. One failing or
  stale cell means the control is not passing. This makes the API row grain
  `control_id`, matching the `/v1/controls/drift` endpoint shape.
  - *Options considered:* per-`(control, scope_cell)` tuple grain (matches the
    *Evidence freshness* KPI's grain) vs per-control rollup. Chose per-control
    because the endpoint is `/v1/controls/drift`, not `/v1/control-cells/drift`,
    and the dashboard "Recent drift" panel lists controls.
  - *Aggregation operator:* worst-cell (canvas §7.3 says the operator is
    explicit per KPI). Worst-cell is the honest choice for a pass/fail rollup
    — a control is only "passing" if it is passing everywhere it applies.
- **"Passing" excludes stale evidence** — `result='pass' AND
  freshness_status='fresh'`. A control whose freshest passing evidence has
  aged out of its window is *drifting*, even though nothing flipped to
  `fail`. This is what makes drift a LEADING indicator and aligns with canvas
  §2.3 ("stale evidence drives a drift signal").
  - *Option considered:* "passing" = `result='pass'` regardless of freshness
    (drift = pure pass→fail transitions). Rejected — that would make freshness
    decay invisible to the drift signal, contradicting §2.3.
- **"Today" vs "yesterday" via persisted daily snapshots.** `delta =
  controls_passing(latest snapshot) − controls_passing(earliest snapshot in
  window)`. The `control_drift_snapshots` table stores one snapshot row per
  refresh; the read path takes the latest row per `(tenant_id, snapshot_date)`.

**Confidence: high.** Grounded directly in canvas §2.3 + §7.1 + §7.3. The
orchestrator reviewed and approved this exact definition before execution
resumed.

### D2 — Two read-model tables, two RLS shapes

- `evidence_freshness` is a materialized current-state read model, UPSERTed
  on every refresh (one row per `(tenant_id, control_id)`). It carries the
  full **four-policy** RLS split (read/write/update/delete) — identical to
  slice 026's `aggregation_rules`.
- `control_drift_snapshots` is an **append-only** daily snapshot ledger —
  drift is a day-over-day diff, so the history must be preserved. It carries
  **two-policy** append-only RLS (`tenant_read` + `tenant_write` only under
  FORCE) — identical to slice 012's `control_evaluations`, slice 013's
  `evidence_audit_log`, slice 026's `aggregation_rule_evaluations`. Every
  refresh APPENDS a row; the read path takes latest-row-per-day.

**Confidence: high.** The slice doc's "materialized read model" framing
explicitly permits both shapes; each table's nature dictates which.

### D3 — Reuse the canvas §2.3 freshness mapping, do not redefine it

The class → max-age table (`realtime` 24h · `daily` 7d · `weekly` 30d ·
`monthly` 90d · `quarterly` 120d · `annual` 400d) already lives in
`internal/eval/state.go` (slice 012). It was unexported. This slice adds an
exported accessor `eval.FreshnessMaxAge(class)` wrapping it, so the table is
defined in exactly one place. The freshness refresh calls that accessor.

**Confidence: high.** Direct instruction from the slice prompt; the only
question was *how* to expose it (export wrapper vs move the table) — chose the
minimal non-breaking export wrapper.

### D4 — Refresh-on-ledger-write via a dedicated NATS subscriber

AC-4 requires the read models refresh "on every ledger write". Implemented as
a third durable JetStream consumer (`evidence_freshness_drift_worker`) on the
slice-015 EVIDENCE_INGEST stream, mirroring slice 012's `eval.IngestSubscriber`.
Three independent durable consumers (015 writes the ledger, 012 evaluates, 016
refreshes the read model) each get every message — no races, no coupling. Plus
a daily 00:00 UTC `Scheduler` tick for time-based recompute (freshness decays
with wall-clock; drift is a day-over-day delta).

The subscriber does a **full-tenant refresh** on every ingested record (not a
per-control incremental refresh). On a busy tenant this is O(active controls)
work per record.

**Confidence: medium.** Correct and simple; the full-tenant refresh is a
deliberate v1 tradeoff for the solo-operator persona (cheap on a single-VM
deployment, single-pathed refresh logic). See revisit list.

## Revisit once in use

- **`control_drift_snapshots.CurrentResult` is always `"not_passing"`.** The
  snapshot ledger records set membership only — it knows a control *left* the
  passing set but not *why* (`fail` vs `stale`). AC-3 asks for "current
  evidence"; the endpoint currently returns `not_passing`. If the dashboard
  needs the fail-vs-stale distinction on the drift panel, enrich the drift
  report by joining the live `control_evaluations` / `evidence_freshness`
  state for each flipped control. Deferred because it adds a join the AC does
  not strictly require and the v1 dashboard mock shows only the control + the
  flip.
- **Per-record full-tenant refresh (D4).** If a tenant ingests at high volume
  (hundreds of records/min), the on-ingest refresh will re-scan all active
  controls per record. Revisit with either (a) a per-control incremental
  refresh keyed on the record's `control_id`, or (b) a debounce/coalesce
  window so a burst of ingests triggers one refresh. Not needed for the
  solo-operator v1 persona; revisit when a connector-heavy tenant appears.
- **Drift "yesterday" when a day has no snapshot.** The window diff uses the
  earliest and latest snapshot *that exist* in the window — if the scheduler
  missed a day (process down across a 00:00 boundary for >1h is caught by the
  hourly tick-check, but a multi-day outage would leave gaps), the delta spans
  whatever days have snapshots. This is a reasonable degradation but the
  endpoint does not currently signal "N days missing". Revisit if operators
  report confusing deltas after an outage.
- **`?bucket=` only supports `class`.** The freshness endpoint hard-rejects any
  other bucketing. Canvas §7.3 mentions per-scope-dimension aggregation
  (by BU / env / geo). When the dashboard needs a by-scope freshness view,
  add `?bucket=scope` rather than overloading `class`.
- **Worst-cell rollup vs the ledger's NULL-cell rows.** Slice 012 writes a
  whole-tenant `scope_cell_id = NULL` evaluation row when a control resolves
  to zero scope cells. The drift `ListPassingControlsForDay` query treats
  `(control_id, NULL)` as one cell-key like any other, so a control with only
  a NULL-cell evaluation rolls up correctly. Revisit if slice 018's
  effective-scope intersection changes how NULL-cell rows are emitted.

## Confidence summary

| Decision | Confidence |
| -------- | ---------- |
| D1 — drift definition (grain, stale-exclusion, snapshot mechanism) | high |
| D2 — two tables, two RLS shapes | high |
| D3 — reuse `eval.FreshnessMaxAge` | high |
| D4 — refresh-on-write subscriber + daily scheduler | medium |
