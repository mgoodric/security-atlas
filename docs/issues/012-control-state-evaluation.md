# 012 — Control state evaluation engine

**Cluster:** Control-as-code
**Estimate:** 2.5d
**Type:** AFK

## Narrative

Build the read-only evaluation stage that consumes the evidence ledger and computes `(control × scope_cell × time) → {pass, fail, na, inconclusive}` state. The engine reads evidence records, applies each control's evidence queries (Rego/SQL/JSON-path), and writes the derived state to a separate `control_state` table — **never** to the evidence ledger. The engine respects each control's `freshness_class`: state is computed over evidence inside the freshness window; older evidence is "stale" not "fail". Effectiveness scores (used by risk derivation in slice 020) are computed here. The slice delivers value because every control in the catalog has an observable pass/fail per scope cell, viewable in the API.

## Acceptance criteria

- [ ] AC-1: `GET /v1/controls/:id/state?scope=<predicate>&as-of=<ts>` returns `{result, evidence_count_in_window, freshness_status, last_observed_at}`
- [ ] AC-2: Background job runs evaluation on every new evidence ingest (NATS consumer from slice 015) and on a schedule for time-based recomputation
- [ ] AC-3: Evaluation is idempotent — running twice over the same evidence produces the same state
- [ ] AC-4: A control with `implementation_type=manual_attested` and a fresh attestation record returns `pass`
- [ ] AC-5: A control whose freshest evidence is older than its `freshness_class` max age is marked `stale` (still queryable; flagged in dashboards)
- [ ] AC-6: Effectiveness score (rolling 30-day pass rate) is computed and exposed at `GET /v1/controls/:id/effectiveness`
- [ ] AC-7: Replay test: deleting `control_state` and running evaluation reproduces identical state from the ledger

## Constitutional invariants honored

- **Invariant 2 (ingestion/eval separated):** engine reads ledger; never writes. State table is the eval output.
- **Invariant 9 (manual evidence first-class):** manual control attestation flows through the same evaluation path

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.3 (ingestion vs evaluation), §4.4 (control-as-code)
- `Plans/canvas/02-primitives.md` §2.3 (freshness model)
- `Plans/canvas/06-risk.md` §6.2 (control_effectiveness math)

## Dependencies

- #010, #013, #017

## Anti-criteria (P0)

- Does NOT write to the evidence ledger
- Does NOT compute state from out-of-window evidence (must respect freshness)
- Does NOT skip the replay-reproducibility property

## Skill mix (3–5)

- Go background workers (NATS subscriber)
- Rego (via OPA Go SDK) for evidence query evaluation
- sqlc for control_state writes
- Postgres window queries (rolling pass rate)
- Idempotent computation patterns
