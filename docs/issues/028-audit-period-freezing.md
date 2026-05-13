# 028 — AuditPeriod + freezing primitive (evidence horizon shift)

**Cluster:** Audit workflow
**Estimate:** 2d
**Type:** AFK

## Narrative

Implement the load-bearing `AuditPeriod` entity and the freezing primitive that solves post-window evidence pollution. When an audit period is frozen, sample populations and control-state queries for that period draw only from evidence records with `observed_at ≤ frozen_at`. Live evaluation continues independently — the auditor's view doesn't shift under their feet. Frozen state is hashed and signed for tamper detection. The append-only ledger makes freezing cheap: we shift the read horizon, no snapshot tables needed. The slice delivers value because a solo operator's first SOC 2 Type II audit can complete without "the dashboard changed between my walkthrough and my report" complaints.

## Acceptance criteria

- [ ] AC-1: `POST /v1/audit-periods` creates a period with: name, framework_version, period_start, period_end, status=open
- [ ] AC-2: `POST /v1/audit-periods/:id/freeze` sets `frozen_at = now`, status=frozen; computes and stores the hash of the frozen state
- [ ] AC-3: `GET /v1/audit-periods/:id/control-state?control=...` returns state computed against `observed_at ≤ frozen_at` only
- [ ] AC-4: Sample populations (slice 026) for a frozen period exclude records observed after `frozen_at`
- [ ] AC-5: Live evaluation (slice 012) is unaffected — current state continues to update
- [ ] AC-6: An attempt to mutate frozen-period state is rejected
- [ ] AC-7: Hash chain: freezing the same content twice produces the same hash

## Constitutional invariants honored

- **Invariant 10 (audit-period freezing):** the entire premise of this slice
- **Invariant 2 (ingestion/eval separated):** freezing is a read-side concept; the ledger itself remains append-only

## Canvas references

- `Plans/canvas/08-audit-workflow.md` §8.4 (audit-period freezing)
- `Plans/canvas/04-evidence-engine.md` §4.3 (separated stages enable replay)

## Dependencies

- #013

(Earlier draft listed #016 as a dependency; freezing uses raw `observed_at` from the ledger and does not require the freshness read-model from slice 016. Dependency dropped per D6 review decision.)

## Anti-criteria (P0)

- Does NOT snapshot evidence tables (the ledger horizon shift is sufficient)
- Does NOT permit retroactive changes to a frozen period's view
- Does NOT skip hash-based tamper detection

## Skill mix (3–5)

- Postgres `observed_at`-bounded queries
- Hash chain / Merkle-like integrity
- Audit-trail discipline
- Go entity workflows
- Time-bound query optimization
