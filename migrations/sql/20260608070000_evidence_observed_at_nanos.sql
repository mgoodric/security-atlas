-- Slice 633 — fix slice 474's residual ingest/verify hash round-trip
-- divergence so the ledger-wide verify walk (slice 464,
-- `atlas evidence verify`) validates production (ingested) records.
--
-- Problem (confirmed by byte-diff in docs/audit-log/633-*-decisions.md):
-- the ingest path stores `hash = canonjson.HashRecord(rec)` over the FULL
-- EvidenceRecord proto, whose `observed_at` is a NANOSECOND-precision
-- google.protobuf.Timestamp. The ledger persists `observed_at` in a
-- `TIMESTAMPTZ` column, which is MICROSECOND precision — so sub-microsecond
-- nanoseconds are truncated on store. The verify walk reconstructs
-- `rec.ObservedAt` from that truncated column and re-hashes; for any record
-- whose observed_at carried sub-microsecond nanos (the CI-Linux case;
-- macOS time.Now() is often microsecond-aligned, which is exactly why this
-- hid in slice 474's local run while failing RED in CI) the recomputed hash
-- diverges from the stored ingest hash.
--
-- Slice 474 fixed the SAME class of divergence for the wire `scope` (it added
-- `scope_canonical` so the verify could reconstruct the scope the hash
-- covered). `observed_at` is the remaining hash-contributing field whose
-- persisted column is lossy. This migration mirrors slice 474's approach.
--
-- Fix (JUDGMENT decision D1, see docs/audit-log/633-*-decisions.md): persist
-- the wire observed_at LOSSLESSLY as a nanosecond integer alongside the
-- record so the verify walk can reconstruct the exact timestamp the ingest
-- hash was computed over. This keeps the hash semantics UNCHANGED — the
-- EVIDENCE_SDK receipt `hash` contract (clients reproduce
-- `proto.MarshalOptions{Deterministic:true}.Marshal(record)` over the
-- nanosecond-precision proto) is preserved. Truncating observed_at before
-- HashRecord (the alternative) would silently change the client-reproducible
-- receipt hash and break the wire contract; rejected.
--
-- Append-only invariant #2 (canvas §4.3): this migration is purely additive.
-- It adds one NULLABLE column. It does NOT rewrite, backfill, or mutate any
-- existing evidence row. Records ingested BEFORE this fix carry
-- `observed_at_nanos = NULL`; the verify walk treats NULL as "legacy row"
-- and falls back to reconstructing observed_at from the lossy `observed_at`
-- TIMESTAMPTZ column (the slice-464/474 baseline), so their existing verify
-- behavior is unchanged. Records ingested AFTER this fix carry the lossless
-- nanosecond value and verify cleanly against their ingest hash.

-- ===== Add the lossless observed-at column =====
--
-- BIGINT holding rec.GetObservedAt().AsTime().UnixNano() — the exact
-- nanosecond-precision instant the content-hash was computed over. NULL =
-- legacy (pre-slice-633) row; the verify walk reconstructs observed_at from
-- the lossy TIMESTAMPTZ column for those, preserving the prior contract.
ALTER TABLE evidence_records
    ADD COLUMN observed_at_nanos BIGINT NULL;

COMMENT ON COLUMN evidence_records.observed_at_nanos IS
    'Slice 633: lossless Unix-nanosecond value of the wire observed_at the '
    'ingest content-hash was computed over, so the `atlas evidence verify` '
    'walk can reconstruct the exact nanosecond timestamp (the observed_at '
    'TIMESTAMPTZ column is microsecond-precision and truncates sub-us nanos). '
    'NULL = pre-slice-633 legacy row (verify reconstructs from the lossy '
    'TIMESTAMPTZ column). Append-only; never mutated post-insert.';
