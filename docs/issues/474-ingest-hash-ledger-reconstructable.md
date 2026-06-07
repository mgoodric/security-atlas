# 474 — align the ingest evidence hash so the ledger-wide verify walk validates production records

**Cluster:** Evidence integrity
**Estimate:** M
**Type:** JUDGMENT
**Status:** `ready`

## Surfaced during slice 464

Slice 464 shipped `atlas evidence verify`, an operator-facing ledger-wide
integrity walk that recomputes each record's canonical hash and compares it
to the stored `hash`. Building it surfaced a load-bearing limitation (slice
464 decisions log D1):

**The ingest hash is not reproducible from a ledger row.** The ingest path
stores `hash = canonjson.HashRecord(rec)` over the **full `EvidenceRecord`
proto including `scope`** (`internal/evidence/ingest/ingest.go:441`). But
the ledger does **not persist the wire `scope`**: there is no scope column,
the push path writes `ScopeID: pgtype.UUID{}` (empty), and scope is absent
from the `provenance` / `source_attribution` JSONB. Confirmed empirically:
for a record with scope, `HashRecord(full)` ≠ `HashRecord(reconstruction
without scope)` ≠ `sha256(payload)`.

Consequently, slice 464's verify reconstructs the record from the persisted
columns (scope omitted) and recomputes — so a record written by the
_current_ ingest path reports a mismatch (its stored hash includes scope the
verify cannot reproduce). Slice 464's AC-3 establishes the baseline by
stamping the ledger-reconstructable hash; that proves the walk's
corruption-detection mechanics but does not make freshly-ingested
production records verify cleanly.

## What to build (pick one — JUDGMENT)

Make the stored `hash` reproducible from the ledger, so
`atlas evidence verify` validates production records out of the box:

1. **Persist the canonical scope** alongside the record (a new column or a
   provenance/JSONB field), and have the verify walk reconstruct scope from
   it before recomputing `canonjson.HashRecord`. Keeps the hash semantics
   (full proto incl. scope) unchanged; the verify becomes faithful. This
   touches the ingest write path + a migration + likely a coverage floor —
   sequence it so it does not collide with whoever owns
   `coverage-thresholds.json` at the time.
2. **Change the stored `hash` to the ledger-reconstructable form** (hash the
   record as it is persisted, scope-free). Simpler verify, but changes the
   tamper-evidence semantics (scope is no longer covered by the per-record
   hash) — weigh against the EVIDENCE_SDK hash contract and the receipt
   `hash` field that pushers may pin. Probably the weaker option; record the
   reasoning either way.

Whichever is chosen, the acceptance bar is: a record pushed via the real
`Process` then walked by `atlas evidence verify` reports **zero mismatches**
on a clean ledger, and still reports a mismatch when any
hash-contributing column is corrupted.

## Acceptance criteria

- [ ] **AC-1.** A record ingested via the real push path verifies clean
      (`atlas evidence verify` reports zero mismatches) — no baseline-stamp
      workaround needed.
- [ ] **AC-2.** Corrupting any hash-contributing persisted column is still
      detected by the walk.
- [ ] **AC-3.** The chosen approach (persist scope vs change hash form) is
      recorded with its trade-off vs the EVIDENCE_SDK hash contract.
- [ ] **AC-4.** If the ingest write path changes, the existing slice-013
      ingest integration tests pass unmodified (or the diff is justified).
- [ ] **AC-5.** Coverage/shard-manifest interactions sequenced to avoid a
      same-batch collision with the then-current owner of
      `coverage-thresholds.json` / `integration-shards.txt`.

## Notes

- Parent: **slice 464** (`atlas evidence verify`). See slice 464 decisions
  log D1 for the empirical confirmation and the options analysis.
- Do not weaken the append-only ledger or the read-only nature of verify
  (canvas invariant #2).
