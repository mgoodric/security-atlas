# 354 — Chaos experiment execution: DB connection-pool exhaustion

**Cluster:** Resilience
**Estimate:** 1d
**Type:** JUDGMENT
**Status:** `ready` — **deferred to v2+** (execution slice)

## Narrative

Executes the chaos experiment designed in slice 335 (Experiment 1 —
Evidence ledger DB connection-pool exhaustion). The design lives at
`docs/audits/335-chaos-experiment-design.md` §Experiment 1. This slice
does NOT redesign the experiment — it picks up the design contract and
performs the controlled failure injection in a local docker-compose
environment.

This slice is **deferred to v2+**. It was filed by slice 335 as a
spillover slot per AC-4 of slice 335. The execution waits until:

1. The synthetic-traffic baseline from slice 332 is stable enough to
   drive the steady-state precondition.
2. The connection-storm primitive `scripts/chaos/db-pool-hog.sh` is
   designed and reviewed (this slice's first deliverable).
3. A maintainer has bandwidth for the high-risk pre-execution
   checklist.

### Why v2+

Slice 335 was design-only. The execution requires writing
`scripts/chaos/db-pool-hog.sh` (executable surface that slice 335
explicitly forbids touching per P0-335-5). This slice carries that
script + the run + the post-experiment report.

### Hypothesis under test

(Pulled verbatim from slice 335 §Experiment 1 for executor convenience.)
When the Postgres connection pool is saturated, evidence reads continue
to succeed at P95 < 5s and writes fail fast with a structured error
(no infinite hang, no stack-trace leakage). The append-only ledger
remains readable.

### High-risk flag (per slice 335 AC-3)

This experiment is one of two flagged high-risk by slice 335. The
pre-execution checklist from the design doc must be satisfied AND
verified by an additional reviewer before injection.

## Threat model

Execution slice; injects controlled failure into local docker-compose.
STRIDE pass:

- **S:** No auth surface. CLEAN.
- **T:** Connection-storming Postgres pool — bounded by docker-compose
  blast radius; no cross-environment impact possible.
- **R:** Experiment outcome logged in
  `docs/audit-log/354-db-pool-exhaustion-execution-decisions.md`.
- **I:** Same Information-disclosure consideration as slice 335.
- **D:** **Load-bearing.** This IS the failure-injection event. The
  design's abort criteria + rollback are the mitigation.
- **E:** Dev-level access.

## Acceptance criteria

- [ ] **AC-1.** `scripts/chaos/db-pool-hog.sh` written; opens N
      connections to docker-compose Postgres and holds them for a
      configurable duration. Code-reviewed by a maintainer with auth-
      reviewer hat on.
- [ ] **AC-2.** Pre-execution checklist from slice 335 §Experiment 1
      satisfied and signed off in the decisions log.
- [ ] **AC-3.** Experiment runs against local docker-compose ONLY.
      Tooling does NOT target atlas-edge or hosted.
- [ ] **AC-4.** Steady-state captured BEFORE injection: P95 read,
      P95 write, error rate over 10 min.
- [ ] **AC-5.** Injection runs for 5 minutes; metrics captured
      throughout.
- [ ] **AC-6.** Recovery measured: time from rollback to baseline P95.
- [ ] **AC-7.** Post-experiment report at
      `docs/audit-log/354-db-pool-exhaustion-execution-decisions.md`
      documenting: hypothesis result (held / falsified), metrics,
      abort triggered (yes / no), recovery time, follow-ups.
- [ ] **AC-8.** Cross-references slice 335 (design) and slice 332
      (baseline).

## Anti-criteria

- **P0-1.** Does NOT target atlas-edge, hosted tenants, or production.
- **P0-2.** Does NOT introduce chaos-mesh / litmus / gremlin as a
  dependency. The script is plain bash + `psql`.
- **P0-3.** Does NOT alter the platform's Postgres pool configuration
  permanently — only injects via external connection-storm.
- **P0-4.** Does NOT auto-merge.

## Dependencies

- **#335** (chaos experiment design) — `merged`. The design contract.
- **#332** (performance audit) — provides the steady-state baseline
  parameters.

## Notes for the implementing agent

1. Read `docs/audits/335-chaos-experiment-design.md` §Experiment 1
   FIRST. Do NOT redesign — execute the design as written.
2. The script `scripts/chaos/db-pool-hog.sh` is this slice's primary
   executable artifact. Keep it under 100 LOC; bash + psql only.
3. The post-experiment report is the audit-binding artifact — it must
   capture whether the hypothesis held or falsified.
4. If the hypothesis FALSIFIES, file a follow-up slice (do not
   silently update the design). A falsification is a finding — the
   platform's invariant claim needs to be amended or the platform
   fixed.
