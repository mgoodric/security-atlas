# 671 — demo seed drives the evaluator so a seeded tenant shows real control state — decisions log

- detection_tier_actual: manual_review
- detection_tier_target: integration

The bug was caught at **manual_review** (the 2026-06-10 demo-tenant UI audit,
item ATLAS-019: a seeded tenant showed every control "—"). It SHOULD have been
caught at the **integration** tier: `internal/demoseed` shipped (slice 205)
with an integration suite that asserts the seed writes ~50 controls + ~200
evidence rows, but had NO assertion that the seeded data is ever EVALUATED —
the suite verified the ledger write, not the downstream read-model state a user
actually sees on `/controls`. The missing assertion is exactly the one this
slice adds (`TestEvaluateSeededTenant_ProducesRealControlState`), which would
have caught the gap the moment slice 205 landed. `actual=manual_review,
target=integration` → an integration-coverage gap in the demoseed suite, now
closed. (The "blank until the evaluator runs" behavior is CORRECT for a live
deployment — ATLAS-029 — so the gap was specifically that the DEMO seed never
ran the evaluator, not an evaluator bug.)

---

## Context

A fully-seeded demo tenant showed STATE / FRESHNESS / LAST-OBSERVED = "—" for
every control, metrics at 0, and an empty "Framework posture". The orchestrator
root-caused it (and this slice verified it): `internal/demoseed/seeder.go`
writes evidence via direct `BYPASSRLS` INSERTs and, by design (the LOAD-BEARING
comment near seeder.go:31), does NOT write `control_evaluations` /
`evidence_freshness`. Production evaluation is driven off the evidence-ingest
JetStream stream (`cmd/atlas/main.go` `eval.NewIngestSubscriber` /
`RefreshSubscriber`) plus the hourly `eval.Scheduler` recompute. The seed
bypasses the ingest stream entirely, so the seeded evidence is never evaluated.

## D1 — Trigger location: a shared post-Apply helper in `demoseed`, called by the two seed call sites (NOT inside the seeder's write tx)

**Decision.** Evaluation is driven by a new exported helper,
`demoseed.EvaluateSeededTenant(ctx, appPool, tenantID)`, called by BOTH seed
call sites (`cmd/atlas-cli/cmd_demo.go runDemoSeed` and
`internal/api/admindemo/handler.go Seed`) AFTER `Seeder.Apply` returns. The
helper is NOT invoked from inside `Seeder.Apply`, and the seeder's
`BYPASSRLS` write transaction is unchanged.

**Why (constitutional invariant #2 — ingestion and evaluation are separated
stages).** The canvas invariant is that evaluation reads the append-only
evidence ledger and never mutates source evidence; a bug in evaluation can
never corrupt the record. Running evaluation as a SEPARATE stage after the seed
write commits is the literal expression of that invariant:

- It does NOT run inside the seed's `BYPASSRLS` write tx (which would entangle
  the two stages and run evaluation with the wrong DB role).
- It does NOT have the demo seed fabricate `control_evaluations` /
  `evidence_freshness` rows directly (the slice's anti-criterion) — that would
  bypass the real evaluator and let demo state diverge from production
  semantics.
- It drives the SAME `eval.Engine.EvaluateAll` + `freshness.Store.Refresh` the
  production scheduler/ingest paths run, so demo state == production state.

A shared helper (rather than duplicating the eval wiring at each call site)
prevents the CLI and HTTP paths from drifting — they call one function.

**Pool choice (invariant #6 — RLS).** The helper takes the **app-role**
(`NOSUPERUSER NOBYPASSRLS`) pool, NOT the seeder's `BYPASSRLS` pool. It binds
the seeded tenant via `tenancy.WithTenant` and runs through
`eval.NewEngineFactory(appPool)` + `freshness.NewStore(appPool)`, both of which
apply `app.current_tenant` as a GUC inside their own transactions. RLS scopes
every read/write to the one seeded tenant; the driver never evaluates across
tenants. Using the `BYPASSRLS` pool here would defeat the invariant.

## D2 — Trigger label passed to EvaluateAll: `TriggerManual`

**Decision.** `EvaluateAll(ctx, eval.TriggerManual, eval.FarFuture)`.

**Why.** The `control_evaluations.trigger` CHECK vocabulary is
`ingest | scheduled | manual | replay`. The demo seed is an explicit operator
action (the operator ran `demo seed` or clicked "Generate demo data"), which is
semantically a manual evaluation — not an ingest-stream reaction (`ingest`), not
the hourly recompute (`scheduled`), not a historical replay (`replay`).
`TriggerManual` records the provenance honestly so a forensic reader of the
ledger can distinguish demo-seed-driven evaluations from production triggers.
`FarFuture` is the live horizon ("all evidence up to now"), matching every
production live-state caller (the scheduler and ingest subscriber both pass
`FarFuture`).

## D3 — Idempotent re-seed handling: evaluate on BOTH the fresh and idempotent paths

**Decision.** The helper is called whether `Apply` returns a fresh result or
`Result.Idempotent == true`. On the idempotent path (tenant already seeded),
evaluation still runs.

**Why.** Re-running `EvaluateAll` is safe — the engine appends one immutable
row per `(control, cell, eval_run)` and the read surfaces project the latest;
`Refresh` UPSERTs one row per control. So a second pass never duplicates state
or corrupts the ledger (AC-5, proven by
`TestEvaluateSeededTenant_Idempotent`). Evaluating on the idempotent path means
a re-click / re-run reliably ENSURES evaluation has happened — which is exactly
the recovery path an operator reaches for if a prior seed's evaluation was
skipped (e.g. the app DSN was unset the first time). It is cheaper to always
re-evaluate (a few hundred ms over ~50 controls) than to add a "has this tenant
been evaluated?" probe whose only payoff is skipping a safe, idempotent
operation.

## D4 — Posture activation (AC-3): evaluation alone does NOT make the tiles render — filed as spillover slice 682

**Decision.** This slice does NOT make "Framework posture" render. The gap is
real, exceeds evaluation wiring, and is filed as **slice 682**
(`docs/issues/682-demo-seed-posture-scf-anchors.md`) rather than silently left.

**What's actually missing (verified against the posture query).** The dashboard
`FrameworkPosture` query (`internal/db/queries/dashboard.sql`) computes coverage
through the SCF-anchor spine (invariant #1):

```
framework_versions (status='current')
  -> framework_requirements
    -> fw_to_scf_edges (STRM, non-no_relationship)
      -> scf_anchors
        <- controls.scf_anchor_id  (the tenant's active controls)
```

The demo seed does NOT satisfy this chain even after evaluation runs:

1. **Demo controls never set `scf_anchor_id`.** `writeControls`
   (`internal/demoseed/writers.go`) inserts `scf_id` (a free-form TEXT label)
   but NOT `scf_anchor_id` (the FK the posture query joins on). So the
   `covering_control` CTE finds zero controls.
2. **The demo framework has no `framework_requirements` and no
   `fw_to_scf_edges`.** When the global SCF catalog is absent, the seed
   synthesizes a bare `frameworks` + `framework_versions` pair (status
   `'current'`) with no requirements and no STRM edges, so `version_reqs` is
   empty.

Both are independent of evaluation: posture stays empty regardless of whether
`control_evaluations` rows exist. Making the tiles render requires the demo seed
to (a) anchor its controls to real SCF anchors and (b) ensure the demo framework
carries requirements + STRM edges (or adopt the global SCF catalog's
requirements when it is loaded). That is a meaningful fixture redesign coupled
to global-catalog presence — out of scope for this slice's evaluation-wiring
core, and correctly a separate JUDGMENT slice. **AC-3 is therefore NOT met by
this slice; it is explicitly deferred to slice 682, not silently dropped.**

**AC-4 (metrics):** the derived metrics that read evaluated state
(program-effectiveness, evidence-freshness) were 0 PURELY because no evaluation
had run. With evaluation now driving `control_evaluations` +
`evidence_freshness`, those metrics compute non-zero where source data exists.
Per-metric formula correctness remains slice 677's job; this slice only
confirms evaluation unblocks the metrics that were 0 for lack of evaluation.

## D5 — Coverage outcomes

- **New pure-Go unit tests** (`internal/demoseed/evaluate_test.go`, no build
  tag, `t.Parallel`): the helper's two guard branches (nil app pool, zero
  tenant id) — the slice-353 pure-Go pre-DB convention. These run on every
  `go test ./...`.
- **New integration tests** (`internal/demoseed/evaluate_integration_test.go`,
  `//go:build integration`, real Postgres, both `DATABASE_URL` +
  `DATABASE_URL_APP`):
  - `TestEvaluateSeededTenant_ProducesRealControlState` — REPRODUCES the
    symptom (0 `control_evaluations`, 0 `evidence_freshness`, every control "—"
    immediately after `Apply`) and asserts the FIX (one evaluation row per
    active control; controls with in-window evidence resolve to concrete
    pass/fail; freshness rows written). Verified to FAIL decisively when the
    helper is neutered (0/50 controls evaluated) and PASS with it (50/50
    controls have an evaluation row; 40 resolve pass/fail — the other 10
    correctly inconclusive because their only evidence is older than their
    freshness window, which is faithful engine behavior).
  - `TestEvaluateSeededTenant_Idempotent` — AC-5: a second pass leaves the
    latest-state read unchanged and the freshness row count stable.
  - `TestEvaluateSeededTenant_GuardsBadInput` — guard branches under the
    integration harness.
- No coverage floor on touched packages was lifted (no threshold change in
  `cmd/scripts/coverage-thresholds.json`), so the ratchet is untouched.

## Verification summary

- `go build ./...`, `go test ./...` (unit), `golangci-lint run` (v2.12.2),
  errleak-lint, duphelper-lint, gofmt — all green on touched packages.
- `go test -tags=integration -p 1 ./internal/demoseed/...` — green (full suite,
  including the slice-205 tests, against a fresh migrated DB).
- `go test -tags=integration -p 1 ./internal/api/admindemo/...` — green (the
  `SetAppPool`-nil path is exercised; evaluation is skipped + logged, non-fatal).
- `git diff origin/main -- migrations/ proto/ schemaregistry/` — empty.
- `%q`-log discipline: the admindemo handler's evaluation log sink `%q`-formats
  the tenant id + error string (CodeQL go/log-injection).
