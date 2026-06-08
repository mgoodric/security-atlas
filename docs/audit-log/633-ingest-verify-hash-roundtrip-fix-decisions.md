# 633 — fix slice 474's residual ingest/verify hash round-trip divergence — decisions log

- detection_tier_actual: production
- detection_tier_target: unit

`TestEvidenceVerify_ProductionRecordValidates_474` failed **deterministically**
on `main` (a PRODUCTION-tier surfacing: the first Go PR — slices 615/620 in
batch 225 — to run `Go · integration (shard A)` to completion since 474 merged
re-caught the accumulated breakage; this is the `CI path-filter as
gap-multiplier` pattern). The bug SHOULD have been caught at the **unit** tier:
slice 474 had no fast pre-DB guard pinning `round-trip(record) re-hash ==
HashRecord(record)`, and its integration test passed locally on macOS (where
`time.Now()` is microsecond-aligned, sub-us = 0) while failing RED in CI on
Linux (full nanosecond resolution). This slice adds that unit guard
(`verify_roundtrip_test.go`, AC-3), so the regression class is now caught
pre-DB, in milliseconds, on every `go test ./...` regardless of host clock
resolution. `actual=production, target=unit` → a unit-coverage gap, now closed.
(The integration tier `target=integration` would also have caught it had 474's
shard-A leg not been merged RED; the unit guard is the stronger fix because it
is host-clock-independent.)

## The confirmed divergent field: `observed_at` (byte-diff evidence)

A pure-Go in-memory reproduction (built the `record(t)` fixture, hashed it,
simulated the ledger round-trip the way ingest persists + verify reconstructs,
re-hashed, and bisected field-by-field) confirmed **`observed_at` is the SOLE
divergent field**. All other hash-contributing fields (payload protojson,
scope canonical JSONB, source_attribution JSONB) round-trip byte-identically.

Byte-diff, with a wire `observed_at` carrying a sub-microsecond nanosecond
component (`...585946123` ns, 123 ns past the microsecond boundary):

```
ingest hash  (full nanos ...946123)        = f4060fd0cefe60174b4b228eb67fb8b2d35854b5ed17daac50beda3776406800
rebuilt hash (us-truncated ...946000)      = d3a6146c62ffd590bf0f0de0222bd542a67d965d06cbb5b6f4e92c6c2afc8b21
after restoring full-nanos observed_at     = f4060fd0... (matches ingest exactly)
```

Restoring the full nanoseconds to the reconstructed record made the re-hash
byte-identical to the ingest hash — proving `observed_at` truncation is the
entire divergence and no other field contributes. The same fixture with a
microsecond-aligned `observed_at` (sub-us = 0) produced NO mismatch — which is
exactly why this hid on macOS dev machines and surfaced only on CI-Linux.

**Mechanism:** the content-hash (`canonjson.HashRecord`) covers the FULL
`EvidenceRecord` proto, whose `observed_at` is a nanosecond-precision
`google.protobuf.Timestamp`. The ledger persisted `observed_at` only in a
`TIMESTAMPTZ` column, which is **microsecond** precision in Postgres. Sub-us
nanoseconds are truncated on store; `RecordFromLedgerRow` reconstructed the
truncated timestamp; the re-hash diverged.

## How slice 474 missed it

Slice 474 fixed the IDENTICAL divergence class for the wire `scope` (it added
the `scope_canonical` JSONB column so the verify could reconstruct the exact
scope the hash covered). It did not enumerate the OTHER lossy-persistence
fields. `observed_at` is the remaining hash-contributing field whose persisted
column is lossy. Two compounding reasons it stayed hidden:

1. **Host clock resolution.** macOS `time.Now()` is frequently
   microsecond-aligned (sub-us nanos = 0), so 474's local integration run
   reconstructed an identical timestamp and passed. CI-Linux carries full
   nanosecond resolution, so its shard-A leg went RED — but 474 was merged
   with that leg RED (per the slice doc's "how it stayed hidden" section).
2. **No host-clock-independent unit guard.** 474's proof was an integration
   test whose pass/fail depended on the wall-clock nanos of the machine that
   ran it. There was no pure-Go test forcing a sub-microsecond timestamp.

## Decisions made

### D1 — Persist faithful `observed_at` losslessly (mirror 474's scope_canonical); do NOT truncate before HashRecord

**This is the load-bearing JUDGMENT call.** Two shapes were possible:
(1) persist the wire `observed_at` losslessly so verify reconstructs the exact
nanosecond timestamp the hash covered, or (2) canonicalize `observed_at` to
microsecond precision _inside_ `HashRecord` (and in the client SDK) so the
stored TIMESTAMPTZ round-trips.

**Chosen: option 1 (persist losslessly).** The deciding factor is the
EVIDENCE_SDK wire contract. `EvidenceReceipt.hash`
(`proto/evidence/v1/evidence.proto:144-152`) is documented as "sha256 of the
deterministic-protobuf-encoded bytes of the `EvidenceRecord` as the server
observed it … Clients reproduce this by calling
`proto.MarshalOptions{Deterministic: true}.Marshal(record)` and hashing the
bytes" — over the **nanosecond-precision** proto. Option 2 would:

- **Break that wire contract.** A client that pins the receipt hash and
  recomputes `HashRecord(record)` over its own nanosecond `observed_at` would
  diverge from a server that truncated to microseconds before hashing. This is
  a CROSS-LANGUAGE contract (Go / Python / protobuf-es); silently changing the
  hashed bytes is exactly the anti-criterion the slice doc blocks merge on.
- **Require an SDK + CLI change in lockstep**, which option 1 avoids entirely.

This is the SAME reasoning slice 474 used to reject the scope-free hash form
(474 D1). The canonical hash input is therefore **unchanged**: the
deterministic-protobuf encoding of the full `EvidenceRecord` with scope sorted
(474) and the nanosecond `observed_at` intact (633). We only changed
PERSISTENCE + RECONSTRUCTION, not the hash function. **Confidence: high.**

### D2 — Mechanism: a dedicated `observed_at_nanos BIGINT` column (not reusing/replacing the TIMESTAMPTZ column)

The existing `observed_at` TIMESTAMPTZ column is load-bearing for the
evaluation read path (`ListEvidenceRecordsByControl ORDER BY observed_at`,
freshness/drift windows). It is microsecond-precision by Postgres design and
keeps its query/index role. A dedicated, purpose-named, nullable `BIGINT`
holding `UnixNano()` is the clearest faithful representation: it carries the
exact instant the hash covered, the verify walk reads exactly one column, and
no existing read path or index changes. (A `TIMESTAMPTZ` cannot hold nanos; an
RFC3339Nano text column would work but a BIGINT UnixNano is the simplest
lossless encoding and is trivial to reconstruct via `time.Unix(0, n)`.)
**Confidence: high.**

### D3 — No backfill; legacy (NULL) rows fall back to the lossy TIMESTAMPTZ column

The evidence ledger is append-only (invariant #2). The new column is nullable
with no default touching existing rows. Records ingested **after** this fix
carry `observed_at_nanos` and verify cleanly against their nanosecond ingest
hash. Records ingested **before** carry NULL; `RecordFromLedgerRow` treats NULL
as a legacy row and reconstructs `observed_at` from the lossy TIMESTAMPTZ
column — exactly the slice-464/474 behavior, so their existing verify baseline
is unchanged (no regression). This is the same legacy-fallback shape 474 used
for `scope_canonical`. In practice there are no real production ledgers yet
(v1 unlaunched), so the legacy path is defense-in-depth, not a live migration
burden. **Confidence: high.**

### D4 — AC-5 client receipt-hash contract is UNCHANGED (verified)

`HashRecord` was not modified. The byte-diff above demonstrates that for a
given record, `HashRecord`'s output is identical before and after this change
(the ingest hash `f4060fd0...` is computed by the unmodified `HashRecord`;
this slice only made the RECONSTRUCTED record re-produce that same hash by
persisting the timestamp faithfully). Therefore the client-reproducible
receipt hash is byte-identical to before; no SDK/CLI change is required. This
is the AC-5 confirmation the slice doc requires.

### D5 — No coverage-thresholds.json change

`internal/evidence/ingest/` is coverage-EXCLUDED (slice 069 exclude policy:
integration-tested against real Postgres; the new pure-Go AC-3 test lives
here). `internal/canonjson` carries a floor of 87 but this slice adds NO
canonjson source (only ingest + verify + a migration + sqlc regen + tests), so
its coverage is unaffected (measured 94.6%, well above the floor). No floor is
lifted. **Confidence: high.**

### D6 — AC-6 (CI guard against merging with a red required shard) spun to a spillover

The slice doc's AC-6 (a CI guard so a slice cannot merge with a required
integration shard RED) is process tooling orthogonal to the integrity fix.
Building it here would broaden a P0 integrity PR into CI-workflow surgery.
Filed as a separate spillover slice in band 631-632 (cites parent 633). This
PR stays focused on the integrity fix. **Confidence: high.**

## What shipped

- `migrations/sql/20260608070000_evidence_observed_at_nanos.sql` (+ `.down.sql`)
  — additive, nullable `observed_at_nanos BIGINT`. Purely additive; no
  backfill; no evidence-row mutation (invariant #2). Reversible (applied,
  down-migrated, re-applied cleanly against Postgres 16). Filename sorts after
  every migration present on this branch (latest was
  `20260608050000_oscal_component_claim_disposition.sql`; the
  `20260608060000_*_scf_mapping.sql` referenced in the spec is from in-flight
  sibling slice 620 and is not on this branch — `...070000` sorts after both).
- `internal/evidence/ingest/ingest.go` — `Process` persists
  `observed.UnixNano()` into the new column alongside the existing hash. Hash
  computation unchanged.
- `internal/evidence/ingest/verify.go` — `RecordFromLedgerRow` reconstructs
  `observed_at` from `observed_at_nanos` when present (lossless), falling back
  to the lossy TIMESTAMPTZ column for legacy NULL rows.
- `internal/db/queries/evidence_ledger.sql` + regenerated `dbx` —
  `InsertEvidenceRecord` writes the new column (`sqlc generate`, pinned
  v1.31.1; dbx never hand-edited; regeneration is deterministic).
- `sqlc.yaml` — new migration enrolled in the schema list.
- `internal/evidence/ingest/verify_roundtrip_test.go` — NEW pure-Go AC-3
  round-trip guard (no DB; host-clock-independent) pinning
  `HashRecord(reconstruct(persist(rec))) == HashRecord(rec)` for a table of
  records incl. the sub-microsecond `observed_at` regression case, plus an
  AC-4 legacy-NULL-row fallback test.
- `internal/evidence/ingest/verify_integration_test.go` — extended: the
  `getEvidenceRowByID` SELECT now reads `observed_at_nanos`, and a new AC-2
  assertion corrupts `observed_at_nanos` out-of-band and asserts detection.
- `CHANGELOG.md` — `[Unreleased]/Fixed` bullet.

## Verification (all run, all green)

- `go build ./...` clean.
- `gofmt -l` empty on all changed files.
- `go vet ./internal/evidence/... ./internal/canonjson/` clean (incl.
  `-tags=integration`).
- `golangci-lint run ./internal/evidence/... ./internal/canonjson/...`: 0
  issues (incl. `--build-tags=integration`).
- Pure-Go AC-3 guard `TestVerifyRoundTrip_PinsHash_AC3` (7 cases) +
  `TestVerifyRoundTrip_LegacyFallback_AC4` PASS. A throwaway "teeth" test
  confirmed the legacy lossy path DIVERGES on sub-us nanos (ingest `ecce5626…`
  vs legacy-rebuilt `e425aa8c…`), proving the guard has teeth.
- Integration (real Postgres 16, `-tags=integration -p 1`):
  `TestEvidenceVerify_ProductionRecordValidates_474` PASS — production record
  verifies clean with NO baseline stamp (AC-1); payload tamper, scope_canonical
  tamper, AND observed_at_nanos tamper all detected (AC-2). The full
  `./internal/evidence/ingest/...` integration suite is green (AC-4: slice-013
  - slice-464 tests unmodified and passing).
- Migration applied + down-migrated + re-applied cleanly (reversible). All
  `migrations/sql/*.sql` apply in order against a fresh Postgres 16 — the
  self-host bundle bring-up applies migrations via the same `*.sql` glob, so
  the bundle legs are covered.
- `go mod tidy` produced no diff; sqlc regeneration is byte-deterministic.

## Spillover filed

- Slice 631 (band 631-632, parent 633): AC-6 — CI guard so a PR cannot merge
  with a required integration shard RED; document the path-filter/concurrency
  masking that let 474 merge with shard-A RED.

## Revisit

- A systematic audit of ALL hash-contributing proto fields vs. their persisted
  column fidelity (scope → fixed by 474; observed_at → fixed here; payload /
  source_attribution confirmed lossless by the AC-3 byte-diff) would close the
  class rather than fixing fields one regression at a time. Noted, not filed —
  the AC-3 round-trip guard now fails loudly if any future field regresses.
