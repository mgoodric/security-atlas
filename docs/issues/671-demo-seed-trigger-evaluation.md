# 671 — Seeded demo tenant shows no evaluated control state / zero metrics (seed never triggers evaluation)

**Cluster:** Demo-seed / Evaluation
**Estimate:** M (1-2d)
**Type:** JUDGMENT (how the seed drives the downstream evaluators)
**Status:** `ready` — surfaced by the 2026-06-10 demo-tenant UI audit (ATLAS-019; cascades 022, 020, 037-posture).

## Narrative

In a fully-seeded demo tenant the Evidence ledger holds 200 records (many `pass`/`fail`),
but **every one of the 53 controls shows STATE / FRESHNESS / LAST OBSERVED = "—"**,
`/controls?state=pass` returns all 53 unchanged (filter no-ops), board metrics read 0, and
"Framework posture" shows "No active framework versions yet". Re-verified on `main` build
`2a3805b` in tenant "Demo Demo".

**Orchestrator root-cause (2026-06-10):** this is **by-design demo-seed behavior, not a
product bug in the evaluators.** `internal/demoseed/seeder.go:31` states the seed
deliberately does NOT write `control_evaluations` / `evidence_freshness` — "those are
computed downstream by the slice-016 evaluator." The evaluator runs as JetStream consumers
bound to the **evidence-ingest (push) stream** (`cmd/atlas/main.go` `eval.NewIngestSubscriber`
/ `RefreshSubscriber`) plus an hourly scheduled recompute (`internal/eval/consumer.go`
`DefaultRecomputeInterval = time.Hour`). The demo seed inserts evidence via **direct
BYPASSRLS writes** — it never publishes to the ingest stream — so **no evaluation/freshness
event ever fires**, and controls/metrics/posture stay empty until (maybe) an hourly tick
that may not cover the seeded tenant. Net: the core value prop is non-demonstrable in the
seeded tenant.

## Threat model

No new data surface. The seed already runs BYPASSRLS with correct tenant_id; triggering an
evaluation pass must stay tenant-scoped and must use the SAME evaluation path as production
(no demo-only shortcut that diverges from real evaluation semantics — invariant #2:
evaluation reads the ledger, never mutates source evidence).

## Acceptance criteria

- [ ] **AC-1.** After `demo seed` completes, the seeded tenant has computed
      `control_evaluations` + `evidence_freshness` for its controls — `/controls` shows real
      STATE / FRESHNESS / LAST OBSERVED (green/red), and `state=pass` filtering works.
- [ ] **AC-2.** JUDGMENT (decisions log): choose how the seed drives evaluation — (a) the
      seeder invokes `Engine.EvaluateAll` (+ freshness recompute) for the tenant as a final
      step, or (b) the seed publishes synthetic ingest events through the normal stream so the
      existing consumers evaluate it. Prefer the path that reuses production evaluation
      semantics with the least divergence. Record the choice.
- [ ] **AC-3.** Framework posture populates: the seeded `framework_versions` are
      active/evaluated so the dashboard "Framework posture" tiles render (resolves the
      posture half of ATLAS-037).
- [ ] **AC-4.** Board/program metrics that derive from evaluated state compute non-zero where
      source data exists (program effectiveness, evidence freshness) — coordinate with slice
      677 (metrics correctness) for the freshness contradiction.
- [ ] **AC-5.** Idempotent: re-running the seed (or a second evaluation pass) does not
      duplicate evaluation rows or corrupt the ledger.

## Anti-criteria

- Does NOT make the seed write evaluation tables DIRECTLY (that would bypass the real
  evaluator and risk demo state diverging from production semantics — AC-2 must drive the
  real engine).
- Does NOT change the production evidence-push→evaluate path.

## Dependencies

- `internal/demoseed` (slice 205) + `internal/eval` (slices 012/016) — on `main`.

## Notes

Source: 2026-06-10 demo-tenant audit, item **ATLAS-019** (high/critical). Root cause shared
with ATLAS-022 (metrics 0), ATLAS-037 (posture), and part of ATLAS-020 (freshness). The
"blank until evaluator runs" behavior is CORRECT for live deployments (see ATLAS-029) — this
slice is specifically that the _demo_ must run the evaluator so the tour isn't misleading.
