# ADR 0012 — Append-only evidence ledger with separated ingestion and evaluation stages

**Status:** Accepted — **retrospective** record of a founding invariant (CLAUDE.md
architecture invariant #2). The decision was made and shipped long before this
ADR; this record reconstructs the trade-off context and the rejected
alternative after the fact. It does NOT re-open the question.

**Date:** 2026-06-04

**Records:** CLAUDE.md architecture invariant **#2** ("Ingestion and evaluation
are separated stages with an append-only evidence ledger between them.
Evaluation never writes to source-of-truth evidence. Bugs in evaluation never
corrupt the record. Point-in-time replay is always possible.").

**Canvas:** [`Plans/canvas/04-evidence-engine.md`](../../Plans/canvas/04-evidence-engine.md) §4.3.

**Implementation reference (cited, not restated):**
[`internal/evidence/`](../../internal/evidence/) (ledger + ingestion stage) and
[`internal/eval/`](../../internal/eval/) (the read-only evaluation stage).

---

## Context

Evidence is the system of record for a security program. When an auditor asks
"prove that this control passed on this date," the answer must be a record that
existed at that date and has not been altered since. The product's binary
success criterion — survive third-party diligence — depends on the evidence
trail being trustworthy in exactly the way a financial ledger is trustworthy:
append-only, replayable, and immune to corruption by downstream processing.

Two distinct activities act on evidence (canvas §4.3):

1. **Ingestion** — a connector emits a raw record; the platform canonicalizes,
   redacts, hashes, scope-tags, and stores it. This is the write path that
   establishes the source of truth.
2. **Evaluation** — controls run queries and policies against stored records to
   produce pass/fail/inconclusive state per `(control × scope × time)`. This is
   a read path that produces a _derived_ result.

The load-bearing question this record answers: **can evaluation ever mutate the
evidence it reads, and can a stored record ever change after it lands?** If the
answer to either is "yes," the audit trail is no longer trustworthy — a bug in
an evaluation rule could rewrite history, and "what did the record say on the
freeze date?" would have no stable answer.

## Decision

**Separate ingestion and evaluation into two stages with an append-only
evidence ledger between them, and forbid evaluation from writing to
source-of-truth evidence.** The data flow is strictly one-directional (canvas
§4.3):

```
Source → Connector → Ingestion stage → [append-only evidence ledger] → Evaluation stage → Control state
```

Three properties follow, and all three are invariant — not best-effort:

1. **The ledger is append-only.** A stored evidence record is immutable; new
   facts arrive as new records, never as edits to existing ones. Correction is
   a new observation, not an overwrite. (Disposal, when it comes, is a
   tombstone, not a mutation — see the data-retention policy; the invariant
   constrains it to tombstones-only.)
2. **Evaluation is a read-only consumer.** The evaluation stage reads the
   ledger and writes only to control state. It has no write path to
   source-of-truth evidence. Therefore **a bug in an evaluation rule can produce
   a wrong control result, but can never corrupt the evidence record** — the
   blast radius of an evaluation bug is bounded to recomputable derived state.
3. **Point-in-time replay is always possible.** Because evidence is immutable
   and carries an `observed_at`, evaluation logic can be re-run against the
   ledger as it stood at any past instant. New controls can be evaluated
   retroactively against existing evidence; a fixed evaluation rule can be
   replayed to correct derived state without touching the record. This is the
   property audit-period freezing (invariant #10) depends on: a frozen sample
   population draws only from records with `observed_at ≤ frozen_at`, and that
   population is stable precisely because the records under it cannot change.

The separation is also the repudiation defense: the ledger is the forensic
anchor for "this evidence existed, unaltered, at this time," and the
content-hash per record (canvas §9) makes tampering detectable.

## Consequences

**Positive:**

- An evaluation bug is always recoverable: fix the rule, replay against the
  unchanged ledger, regenerate control state. The record of truth is never the
  casualty.
- "What did the evidence say on the audit freeze date?" has a stable,
  defensible answer — the foundation of audit-period freezing.
- New controls evaluate retroactively against existing evidence with no
  re-collection — coverage for a newly-added framework requirement can be
  computed from records already in the ledger.
- The append-only shape gives the audit trail the same trust properties a
  reviewer expects from a financial ledger.

**Negative / accepted trade-offs:**

- **Storage grows monotonically.** Corrections add records rather than
  replacing them; the ledger never shrinks through normal operation. Accepted:
  this is the price of replayability, and large-artifact bodies (> 1 MB) go to
  S3-compatible object storage (canvas §9) rather than inline.
- **"Update" is not a primitive.** Code that wants to "fix" an evidence record
  must append a new observation, and consumers must read the latest. This is
  more ceremony than a mutable row, and it is deliberate — the ceremony is the
  guarantee.
- **Two stages are more moving parts than one.** The append-only contract
  between them must be enforced (evaluation has no evidence-write grant) rather
  than merely intended. Accepted: the separation is what bounds an evaluation
  bug's blast radius, and the canvas treats it as non-negotiable.
- **Disposal is constrained.** Hard-deleting a record for retention/erasure
  would violate append-only; the data-retention policy is therefore
  tombstone-based rather than row-deleting, which is a more complex erasure
  story than `DELETE`. Accepted as the cost of an un-rewritable ledger.

## Alternatives considered (rejected — recorded retrospectively)

- **A single mutable evidence table that evaluation updates in place
  (status/result columns written back onto the evidence row).** Rejected. It
  collapses the source-of-truth record and the derived result into one row, so
  an evaluation bug rewrites history and point-in-time replay becomes
  impossible — there is no "as it stood then" to replay against. This is the
  concrete alternative invariant #2 exists to forbid.
- **Mutable evidence with an external audit log of changes.** Rejected. An
  audit-log-of-mutations reconstructs history at the cost of trusting the log
  to be complete and itself un-tampered — strictly weaker than never mutating
  the record in the first place. Append-only makes the record _be_ the audit
  trail rather than needing a second one to watch it.
- **Compaction / squashing of old evidence to bound storage growth.**
  Rejected as a violation of the invariant: "the ledger may be compacted" would
  document a weaker guarantee than the system enforces and would break
  point-in-time replay (a compacted-away record cannot be replayed). Growth is
  managed by object-storage offload and retention tombstones, never by
  rewriting or dropping the ledger's history.
- **Coupling ingestion and evaluation into one synchronous write path** (a
  connector push that immediately computes and persists control state in the
  same transaction). Rejected. It makes evaluation latency a gate on ingestion
  throughput, prevents replaying evaluation independently, and re-introduces
  the write-back coupling the separation exists to remove.

## Related decisions

- Underpins audit-period freezing (invariant #10): the frozen sample
  population is stable only because the ledger beneath it is immutable.
- Composes with **ADR-0011** (RLS): evidence records are tenant-scoped rows
  under the same RLS boundary; append-only and tenant-isolated are orthogonal
  guarantees that both hold.
- Composes with the evidence content-hash + cosign export signing (canvas §9,
  ADR-0010): the hash makes a tampered record detectable; append-only makes
  tampering pointless because the original record still stands.
