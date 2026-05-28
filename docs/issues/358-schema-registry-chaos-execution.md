# 358 — Chaos experiment execution: schema-registry unavailable

**Cluster:** Resilience
**Estimate:** 1d
**Type:** JUDGMENT
**Status:** `ready` — **deferred to v2+** (execution slice)

## Narrative

Executes the chaos experiment designed in slice 335 (Experiment 7 —
Schema-registry unavailable). The design lives at
`docs/audits/335-chaos-experiment-design.md` §Experiment 7. This slice
does NOT redesign the experiment — it picks up the design contract and
performs the controlled failure injection.

The experiment verifies that ingest fails-fast on an unknown
`evidence_kind` rather than accepting an unvalidated payload — a
load-bearing invariant for ledger quality. It also verifies the
hot-cache decoupling: known kinds continue to ingest even with the
registry down.

This slice is **deferred to v2+**. It was filed by slice 335 as a
spillover slot per AC-4. It is **standalone** (not bundled) because
the injection mechanism is schema-registry-shape-specific and the
hot-cache-vs-cold-miss verification distinguishes warrants its own
decisions log.

### Why v2+

Slice 335 was design-only. Execution requires confirming the
schema-registry's current deployment shape (in-process Go service
per slice 068 v1, or possibly promoted to a sidecar in a later
slice) and adapting the injection mechanism accordingly.

### Hypothesis under test

(Pulled verbatim from slice 335 §Experiment 7 for executor convenience.)
When the schema registry is unavailable, evidence push for an unknown
`evidence_kind` returns 503 with structured error. Push for a known,
hot-cached kind continues to succeed.

## Threat model

Execution slice; injects controlled failure into schema-registry.
STRIDE pass:

- **S:** No auth surface. CLEAN.
- **T:** Registry stop — bounded by docker-compose blast radius.
- **R:** Outcome logged in
  `docs/audit-log/358-schema-registry-chaos-execution-decisions.md`.
- **I:** Same as slice 335.
- **D:** **Load-bearing.** This IS the failure-injection event.
- **E:** Dev-level access.

## Acceptance criteria

- [ ] **AC-1.** Schema-registry deployment shape confirmed against
      slice 068 (in-process v1) and any later slice that may have
      promoted it.
- [ ] **AC-2.** Pre-execution checklist from slice 335 §Experiment 7
      satisfied (known-kind pre-pushed to populate hot-cache).
- [ ] **AC-3.** Steady-state captured BEFORE: known-kind push 2xx,
      unknown-kind push 400 with `evidence_kind_not_found`.
- [ ] **AC-4.** Registry process / container stopped.
- [ ] **AC-5.** Known-kind push: must still succeed (hot-cache check).
- [ ] **AC-6.** Unknown-kind push: must return 503 with
      `schema_registry_unavailable` — distinguishable from the
      steady-state 400 `evidence_kind_not_found`.
- [ ] **AC-7.** Registry restored; unknown-kind push transitions
      back to 400.
- [ ] **AC-8.** Post-experiment report at
      `docs/audit-log/358-schema-registry-chaos-execution-decisions.md`.
- [ ] **AC-9.** Cross-references slice 335 (design) and slice 068
      (schema-registry shape).
- [ ] **AC-10.** If unknown-kind push returns 2xx during the outage,
      file a critical-finding slice — this is a ledger-quality
      breach.

## Anti-criteria

- **P0-1.** Does NOT target atlas-edge or hosted.
- **P0-2.** Does NOT introduce a chaos framework as a dependency.
- **P0-3.** Does NOT modify the registry's hot-cache policy
  permanently — the test verifies the existing contract.
- **P0-4.** Does NOT auto-merge.

## Dependencies

- **#335** (chaos experiment design) — `merged`. The design contract.
- **#068** (schema-registry evidence_kind fix) — defines the
  registry shape.

## Notes for the implementing agent

1. Read `docs/audits/335-chaos-experiment-design.md` §Experiment 7
   FIRST.
2. The schema-registry shape may have shifted since slice 068 — confirm
   the current shape before designing the injection mechanism.
3. If the hot-cache-decoupling claim FALSIFIES (known-kind push fails
   during registry outage), the cache contract is broken — file an
   architecture-finding slice.
4. If the fail-fast claim FALSIFIES (unknown-kind push returns 2xx
   during outage), this is **critical** — the ledger could accept
   unvalidated data. STOP, restore the registry, file immediately.
