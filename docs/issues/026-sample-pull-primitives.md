# 026 — Sample-pull primitives (Population + Sample with deterministic seed)

**Cluster:** Audit workflow
**Estimate:** 1.5d
**Type:** AFK

## Narrative

Implement the sampling primitives that auditors use to test controls. `Population(control, scope_predicate, time_window)` is a deterministic set of evidence records or applicable entities (e.g., users, devices) drawn from the ledger or domain tables. `Sample(population, n, seed)` returns N members deterministically; re-running with the same seed returns the same N members (critical for auditor reproducibility). Auditors can also accept a sample as "tested" with a finding annotation. The slice delivers value because auditors stop pulling samples in Excel — they pull from the platform with reproducible seeds.

## Acceptance criteria

- [ ] AC-1: `POST /v1/populations` creates a population (control_id, scope_predicate, time_window) and returns the row count
- [ ] AC-2: `POST /v1/samples` creates a Sample(population_id, n, seed) and returns the N records; rerunning returns identical N
- [ ] AC-3: Sample is recorded as an artifact with `population_id`, `n`, `seed`, `created_by`, `created_at`
- [ ] AC-4: Tested samples can carry a `result=passed | failed | not-applicable` annotation per record
- [ ] AC-5: Samples respect audit-period freezing — populations only include records with `observed_at ≤ frozen_at`
- [ ] AC-6: Audit log records every sample pull with the same `seed → sample` mapping for re-audit

## Constitutional invariants honored

- **Invariant 10 (audit-period freezing):** populations honor frozen evidence horizon
- **Invariant 2 (ingestion/eval separated):** samples read from ledger; never mutate ledger

## Canvas references

- `Plans/canvas/08-audit-workflow.md` §8.3 (sample-pull primitives)

## Dependencies

- #013, #017

## Anti-criteria (P0)

- Does NOT permit non-deterministic sampling (must always reproduce given same seed)
- Does NOT permit samples drawn from post-frozen evidence
- Does NOT delete or modify sampled records

## Skill mix (3–5)

- Go (deterministic PRNG with seed)
- Postgres set operations
- sqlc + materialized views for population caching
- Auditor workflow domain modeling
- Audit-log discipline
