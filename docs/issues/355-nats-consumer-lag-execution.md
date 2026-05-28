# 355 — Chaos experiment execution: NATS JetStream consumer lag spike

**Cluster:** Resilience
**Estimate:** 1d
**Type:** JUDGMENT
**Status:** `ready` — **deferred to v2+** (execution slice)

## Narrative

Executes the chaos experiment designed in slice 335 (Experiment 2 —
NATS JetStream consumer lag spike). The design lives at
`docs/audits/335-chaos-experiment-design.md` §Experiment 2. This slice
does NOT redesign the experiment — it picks up the design contract and
performs the controlled failure injection.

This experiment is the **highest criticality** of the eight in slice
335 because it directly verifies constitutional invariant #2
(separation of ingestion and evaluation stages). If the hypothesis
falsifies, the two-stage architecture's core claim is in question.

### Why v2+

Slice 335 was design-only. Execution requires the synthetic-ingest
generator from slice 332's load-test surface to be stable. It also
requires the operator's runbook for `nats consumer pause` / `resume`
to be confirmed against the production NATS deployment shape.

### Hypothesis under test

(Pulled verbatim from slice 335 §Experiment 2 for executor convenience.)
When the evaluation consumer is paused, ingest continues at baseline.
The ledger absorbs new records. Eval backlog grows linearly with
input. On consumer resume, the backlog drains without data loss.

## Threat model

Execution slice; injects controlled failure into local docker-compose
NATS. STRIDE pass:

- **S:** No auth surface. CLEAN.
- **T:** Pausing a durable consumer — bounded by docker-compose blast
  radius.
- **R:** Experiment outcome logged in
  `docs/audit-log/355-nats-consumer-lag-execution-decisions.md`.
- **I:** Same Information-disclosure consideration as slice 335.
- **D:** **Load-bearing.** This IS the failure-injection event.
- **E:** Dev-level access.

## Acceptance criteria

- [ ] **AC-1.** Synthetic-ingest generator running at slice 332
      baseline (10/s). If the baseline has shifted, re-derive.
- [ ] **AC-2.** Pre-execution checklist from slice 335 §Experiment 2
      satisfied (`ack_wait` configured > 10 min; consumer snapshot
      captured).
- [ ] **AC-3.** Experiment runs against local docker-compose ONLY.
- [ ] **AC-4.** Steady-state captured BEFORE injection: push P95,
      eval P95, consumer-pending count.
- [ ] **AC-5.** Consumer paused for 10 minutes; metrics captured
      throughout. The **push API P95 must not change** — this is the
      falsification check.
- [ ] **AC-6.** Consumer resumed; drain time measured to zero
      pending.
- [ ] **AC-7.** Post-experiment report at
      `docs/audit-log/355-nats-consumer-lag-execution-decisions.md`
      documenting: hypothesis result (HELD = invariant #2 confirmed
      under chaos; FALSIFIED = serious finding), metrics, follow-ups.
- [ ] **AC-8.** Cross-references slice 335 (design), slice 332
      (baseline), and `Plans/canvas/04-evidence-engine.md` §4.3
      (the invariant being verified).
- [ ] **AC-9.** If FALSIFIED, file an architecture-finding slice
      immediately — do NOT silently amend the canvas.

## Anti-criteria

- **P0-1.** Does NOT target atlas-edge, hosted tenants, or production.
- **P0-2.** Does NOT introduce chaos-mesh / litmus as a dependency.
- **P0-3.** Does NOT permanently alter the NATS stream configuration.
- **P0-4.** Does NOT auto-merge.

## Dependencies

- **#335** (chaos experiment design) — `merged`. The design contract.
- **#332** (performance audit) — provides synthetic-ingest baseline.
- **#015** (NATS JetStream) — provides the durable consumer surface.

## Notes for the implementing agent

1. Read `docs/audits/335-chaos-experiment-design.md` §Experiment 2
   FIRST. Do NOT redesign.
2. This experiment falsifies the invariant if push P95 changes when
   eval is paused. Capture push P95 every second during the pause
   window so the falsification signal cannot hide in averaged
   metrics.
3. If the hypothesis HOLDS, this is the strongest single piece of
   verification evidence the project has for invariant #2 — record
   it carefully in the decisions log.
