# 474 — align the ingest content-hash so the ledger verify walk validates production records — decisions log

- detection_tier_actual: production
- detection_tier_target: integration

The mismatch this slice fixes surfaced on PRODUCTION (ingested) records:
slice 464's `atlas evidence verify` walk could not validate any record
written by the real `Process` path — every freshly-ingested record reported
a mismatch. Slice 464's AC-3 integration test masked the gap by stamping a
ledger-reconstructable baseline hash before asserting (so the test passed
while the production behavior was broken). The bug SHOULD have been caught at
the **integration** tier by a "push via real `Process` → run verify →
assert clean" assertion — which slice 464 did not have and this slice adds
(`TestEvidenceVerify_ProductionRecordValidates_474`). `actual=production,
target=integration` → an integration-coverage gap, now closed.

## What shipped

- `migrations/sql/20260608040000_evidence_scope_canonical.sql` (+ `.down.sql`)
  — additive, nullable `scope_canonical JSONB` column on `evidence_records`.
  Purely additive; no backfill; no evidence-row mutation (invariant #2).
- `internal/canonjson/canonjson.go` — `MarshalCanonicalScope` /
  `UnmarshalCanonicalScope`: the ONE shared codec for the canonical wire
  scope, so ingest (writing the column) and the verify walk (reading it
  back) agree by construction. The marshal sorts scope identically to
  `HashRecord`'s normalization.
- `internal/evidence/ingest/ingest.go` — `Process` now persists the
  canonical scope into the new column alongside the existing scope-inclusive
  hash. The hash computation is unchanged.
- `internal/evidence/ingest/verify.go` — `RecordFromLedgerRow` rehydrates
  `rec.Scope` from `scope_canonical` before recomputing the hash (NULL → the
  slice-464 scope-free fallback for legacy rows).
- `internal/db/queries/evidence_ledger.sql` + regenerated `dbx` —
  `InsertEvidenceRecord` writes the new column (`sqlc generate`, pinned
  v1.31.1; dbx never hand-edited).
- `sqlc.yaml` — new migration enrolled in the schema list (the column must be
  visible to sqlc).
- Tests: `internal/canonjson/canonjson_test.go` (pure-Go round-trip,
  ordering-normalization, NULL/legacy, decode-error) +
  `internal/evidence/ingest/verify_integration_test.go`
  (`TestEvidenceVerify_ProductionRecordValidates_474`: ingest → verify clean
  with NO baseline stamp + payload tamper detected + scope tamper detected).
- `CHANGELOG.md` — `[Unreleased]/Added` bullet.

## Decisions made

### D1 — Canonical hash input: KEEP the full scope-inclusive proto hash; persist scope (option 1), do NOT change the hash form (option 2)

**This is the load-bearing JUDGMENT call.** The slice doc offered two shapes:
(1) persist the canonical wire scope so the verify can reconstruct the exact
record the scope-inclusive ingest hash was computed over, or (2) change the
stored hash to the ledger-reconstructable (scope-free) form.

**Chosen: option 1 (persist scope).** The deciding factor is the EVIDENCE_SDK
wire contract. The proto `EvidenceReceipt.hash` field
(`proto/evidence/v1/evidence.proto:144-152`) is documented as "sha256 of the
deterministic-protobuf-encoded bytes of the `EvidenceRecord` as the server
observed it … Clients reproduce this by calling
`proto.MarshalOptions{Deterministic: true}.Marshal(record)` and hashing the
bytes" — i.e. the full record **including scope**. A pusher may pin the
receipt hash. Option 2 would:

- **Break that wire contract** — a client that recomputes
  `HashRecord(record-with-scope)` and compares to the receipt hash would
  diverge.
- **Drop scope from the per-record tamper envelope** — scope would no longer
  be covered by the hash, weakening tamper-evidence (a silent scope mutation
  would go undetected). The new scope-tamper assertion in the integration
  test demonstrates exactly the protection option 2 would have lost.

Option 1 keeps the hash semantics identical (canvas evidence-integrity:
"sha256 content-hash per record") and makes the verify faithful by recording
the hash's scope input in the ledger. The canonical hash input is therefore:
**the deterministic-protobuf encoding of the full `EvidenceRecord` with scope
sorted by key and values sorted lexicographically** — unchanged from before
this slice. **Confidence: high.**

### D2 — Persistence mechanism: a dedicated `scope_canonical JSONB` column (not scope_id, not provenance)

`scope_id` is a UUID FK to the `scopes` table — it models the platform's
internal multidimensional scope cell, not the connector's wire scope
`[]ScopeDimension`; reusing it would conflate two concepts and the push path
has no scope-cell UUID to write. Folding scope into the existing
`provenance` / `source_attribution` JSONB would overload server-observed
provenance with hash-input data and make the verify reconstruction read a
sub-field of an unrelated blob. A dedicated, purpose-named, nullable JSONB
column is the clearest faithful representation: the verify walk reads exactly
one column, and the column's only job is "the scope the hash covered".
**Confidence: high.**

### D3 — Existing-records reconciliation: NO backfill; legacy (NULL) rows fall back to the slice-464 scope-free reconstruction

The evidence ledger is append-only (invariant #2: "do NOT rewrite/corrupt
existing evidence records"). A backfill migration that recomputed and updated
stored hashes — or that populated `scope_canonical` on historical rows — would
violate that, and is also impossible for true legacy rows because the wire
scope was never persisted (it cannot be reconstructed). So:

- The new column is **nullable with no default touching existing rows**.
- Records ingested **after** this fix carry `scope_canonical` and verify
  cleanly against their scope-inclusive ingest hash.
- Records ingested **before** this fix carry `scope_canonical = NULL`; the
  verify walk treats NULL as a legacy row and reconstructs scope-free —
  exactly the slice-464 behavior, so their existing verify baseline is
  unchanged (no regression for pre-fix rows).

This is honest and append-only-safe: the fix makes new production records
verify out of the box (the slice's acceptance bar) without rewriting one byte
of history. In practice there are no real production ledgers yet (v1 not
launched), so the legacy-row path is defense-in-depth, not a live migration
burden. **Confidence: high.**

### D4 — Shared codec in `internal/canonjson`, not duplicated in ingest + verify

The marshal/unmarshal lives once in `canonjson` (the canonical home of the
hash derivation) so ingest and verify cannot drift. Layering is preserved:
`ingest` and `verify` already depend on `canonjson`; `canonjson` depends on
neither. The marshal sorts identically to `HashRecord`'s normalization, so a
record's persisted `scope_canonical` describes precisely the scope the hash
covered. **Confidence: high.**

### D5 — No coverage-thresholds.json / integration-shards.txt change (AC-5)

`internal/canonjson` and `internal/evidence/ingest` are both coverage-EXCLUDED
(slice 464 D1 placed `verify.go` in ingest for exactly this reason), so no
floor is lifted and there is no collision with sibling batch-222 slice 520's
ownership of `coverage-thresholds.json`. `internal/evidence/ingest` is already
enrolled in `scripts/integration-shards.txt` (Leg A), so no manifest change
and `scripts/audit-integration-enrolment.sh` stays green. **Confidence: high.**

## Verification (all run, all green)

- `go build ./...` clean.
- `go test ./internal/evidence/... ./internal/canonjson/` green (unit incl.
  the canonical-scope round-trip + NULL/legacy + decode-error cases).
- `go test -tags=integration -p 1 ./internal/evidence/...` green against real
  Postgres: `TestEvidenceVerify_ProductionRecordValidates_474` proves a
  record pushed via the real `Process` verifies clean with NO baseline stamp
  (AC-1), payload tamper detected, AND scope_canonical tamper detected (AC-2,
  tamper-evidence preserved). The pre-existing slice-464 AC-3 test passes
  unmodified, as do all slice-013 ingest integration tests (AC-4).
- Migration applied + dropped + re-applied cleanly against Postgres 16
  (reversible).
- `golangci-lint run` on the changed packages: 0 issues.

## Revisit

- A `--repair`/`--rebaseline` verify flag that stamps the
  ledger-reconstructable hash for legacy NULL-scope rows (operator-initiated,
  audit-logged) could let an operator opt a historical ledger into clean
  verification — out of scope here (it touches the append-only posture and
  wants its own review). Not filed; noted.
