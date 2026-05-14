# 020 — Risk-control linkage + residual derivation — decisions log

Slice 020 is `Type: AFK` in its frontmatter, but the grill against
`Plans/canvas/06-risk.md §6.2` surfaced two architecture-interpretation
calls that are genuine build-time judgments. This log records them in
the JUDGMENT-slice format so the maintainer can re-evaluate once the
residual pipeline is in real use. Neither call blocks merge.

## Decisions made

### 1. AC-5's "control_state change via NATS" → subscribe to `evidence.ingest`, not a control-state-change subject

**The drift.** AC-5 reads: _"Residual recomputes within 60 seconds of
any control_state change (via NATS subscriber)."_ The narrative says
_"Residual recomputes whenever control state changes (event-driven via
NATS)."_ Both phrasings imply there is a control-state-change event to
subscribe to.

**Codebase reality.** There is not. Slice 012's `internal/eval`:

- `IngestSubscriber` _consumes_ slice 015's `evidence.ingest` JetStream
  stream and writes `control_evaluations` — it publishes nothing back to
  NATS.
- A control-state change leaves no event on any subject; it is just a
  new row appended to `control_evaluations`.

So there is no `control.state.changed` subject. The _cause_ of every
control-state change is a new evidence record landing on slice 015's
`evidence.ingest` stream.

**Options considered:**

- **(A) Add a `control.state.changed` publisher to slice 012.** Slice
  012 would publish an event on every evaluation. Slice 020 subscribes
  to that.
- **(B) Subscribe to `evidence.ingest` directly.** Bind a third durable
  JetStream consumer to the same stream slice 012's `IngestSubscriber`
  already consumes. On each ingested record, recompute residual for the
  risks linked to the affected control.

**Chosen: (B).**

**Rationale.** (A) rewrites slice 012's contract — a cross-slice change
that is out of scope for 020 and risk-laden (slice 012 is the merged
critical-path keystone). (B) mirrors the exact pattern slice 012 itself
established: a second durable consumer on `evidence.ingest`. Slice 020
adds a third (`risk_residual_worker`). Three independent durable
consumers on a Limits-retention stream each get every message — the
residual recompute never races or blocks the ledger writer or the
evaluation reaction. AC-5's wording describes _intent_ ("residual tracks
control state"); `evidence.ingest` is the architecturally faithful
trigger for that intent.

**Confidence: high.** The pattern is proven (slice 012 ships it), the
invariant-2 boundary holds (the subscriber reads `control_evaluations` +
`risk_control_links`, writes only `risks.residual_score`), and the
60-second budget is comfortable.

**Revisit once in use** if slice 012 ever does grow a
`control.state.changed` publisher (e.g. for a UI live-update feed) — at
that point the residual subscriber could move to the narrower subject to
avoid recomputing on evidence ingests that did not flip any control's
state. Not worth pre-building.

### 2. EvaluateControl-first race fix in the residual subscriber

**The race.** Slice 012's `IngestSubscriber` and slice 020's
`ResidualSubscriber` both bind durable consumers to `evidence.ingest`,
so both fire on the same message, concurrently. The residual subscriber
could read `control_evaluations` _before_ slice 012's subscriber has
written the new evaluation row — recomputing residual off stale control
state.

**Options considered:**

- **(A) Accept eventual consistency.** Rely on slice 012's hourly
  `Scheduler` sweep to eventually re-evaluate, and the next evidence
  ingest to eventually recompute residual off fresh state.
- **(B) The residual subscriber re-evaluates the control itself first.**
  `ResidualDeriver.DeriveAndPersist` is called with `recompute=true`,
  which calls `eval.Engine.EvaluateControl` on each linked control
  _before_ reading its effectiveness.

**Chosen: (B).**

**Rationale.** (A) makes AC-6 ("control flips pass→fail, residual
visibly increases on next query") flaky — the increase might lag a full
scheduler tick. (B) eliminates the race deterministically:
`EvaluateControl` is idempotent (slice 012's own contract — append-only
ledger, latest-by-`evaluated_at` wins, computed result is a
deterministic function of the ledger slice), so the extra evaluation row
is harmless, and the residual always reflects the just-ingested record.
The read path (`GET /v1/risks/{id}`) passes `recompute=false` — a read
must not trigger evaluation. Only the subscriber and the link endpoint's
immediate recompute use it; the link endpoint passes `false` because the
linked control's state did not change at link time.

**Confidence: high.** The integration test
`TestResidualSubscriber_RecomputesOnEvidenceIngest` exercises the full
path against real Postgres + real NATS and confirms residual settles to
the correct value; `TestResidualSubscriber_RedeliveryIsIdempotent`
confirms a double delivery does not corrupt or double-apply.

**Revisit once in use** if the extra `EvaluateControl` call per linked
control per ingest becomes a measurable cost at higher cardinality than
v1's solo-security-lead target. The fix would be a coordination
primitive (e.g. the residual subscriber consumes a slice-012-published
"evaluation committed" event instead) — but that is decision #1's
option (A) and is explicitly deferred.

## Smaller calls (recorded for completeness)

- **`risk_control_links` weight columns, not a sibling table.** The
  canvas §6.2 effectiveness formula needs `design_score` + three weights
  per link. These went on the existing `risk_control_links` table
  (migration `_029` ALTER) rather than a new `risk_control_link_weights`
  table — they are 1:1 with the link, never queried independently, and
  the four-policy RLS split already covers them (RLS is row-scoped).
- **Only human inputs persisted; operational + coverage derived at read
  time.** `operational_score` (slice 012 rolling pass rate) and
  `coverage_score` (passing cells / applicable cells) are computed on
  every `Derive` call, never stored on the link row. Caching a derived
  score beyond its staleness threshold is a P0 anti-criterion; only
  `design_score` and the weights (genuine human inputs) are persisted.
- **`residual_score` JSONB shape.** The derived residual is written to
  the existing slice-002 `risks.residual_score` JSONB column as
  `{score, inherent_score, weighted_control_effectiveness, breakdown[],
warning?}` — no `risks`-table ALTER was needed, so no `mustInsertRisk`
  fixture patch (unlike slices 019/018/009).
- **Stale slice-019 test fixture patched.** `internal/risk/
integration_test.go`'s `seedControl` helper was missing `bundle_id`
  (NOT NULL since slice 009's migration, which landed after slice 019
  authored the helper). Patched in-place — same stale-fixture precedent
  as slices 019/018/009.
- **`inherentScalar` reduction.** The §6.2 formula multiplies a scalar
  inherent score; methodology-specific `inherent_score` JSONB is reduced
  to a scalar — `likelihood × impact` for nist_800_30 / qualitative_5x5,
  `lef × lm` for FAIR, a precomputed `severity` field when present
  (slice 053 aggregated parents). Methodologies without a scalar shape
  and no `severity` field return a clear error rather than a silent 0.
