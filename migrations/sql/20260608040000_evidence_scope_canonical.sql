-- Slice 474 — align the ingest content-hash so the ledger-wide verify walk
-- (slice 464, `atlas evidence verify`) validates production (ingested)
-- records.
--
-- Problem (slice 464 decisions log D1, confirmed empirically): the ingest
-- path stores `hash = canonjson.HashRecord(rec)` over the FULL EvidenceRecord
-- proto INCLUDING the wire `scope`, but the ledger never persisted that
-- wire scope (the push path writes `scope_id = NULL`/empty and scope is
-- absent from provenance / source_attribution). The verify walk reconstructs
-- the record from the persisted columns (scope-free) and recomputes — so a
-- freshly-ingested production record reports a mismatch because its stored
-- hash covers scope the verify cannot reproduce.
--
-- Fix (JUDGMENT decision D1, see docs/audit-log/474-*-decisions.md): persist
-- the canonical (sorted) wire scope alongside the record so the verify walk
-- can reconstruct the exact record the ingest hash was computed over. This
-- keeps the hash semantics UNCHANGED — the EVIDENCE_SDK receipt `hash`
-- contract (clients reproduce `HashRecord(record-with-scope)`) is preserved.
-- The alternative (changing the stored hash to a scope-free form) would
-- break that wire contract and drop scope from the per-record tamper
-- envelope; rejected.
--
-- Append-only invariant #2 (canvas §4.3): this migration is purely additive.
-- It adds one NULLABLE column. It does NOT rewrite, backfill, or mutate any
-- existing evidence row. Records ingested BEFORE this fix carry
-- `scope_canonical = NULL`; the verify walk treats NULL as "legacy row" and
-- falls back to the slice-464 scope-free reconstruction for those rows, so
-- their existing verify baseline is unchanged. Records ingested AFTER this
-- fix carry the canonical scope and verify cleanly against their
-- scope-inclusive ingest hash.

-- ===== Add the canonical-scope column =====
--
-- JSONB holding the canonical (sorted-by-key, values-sorted) wire scope:
--   [{"key":"environment","values":["prod"]}, ...]
-- This mirrors exactly what canonjson.HashRecord normalizes before hashing,
-- so the verify walk can rehydrate `rec.Scope` and recompute an identical
-- hash. NULL = legacy (pre-slice-474) row; the verify walk reconstructs
-- scope-free for those, preserving the slice-464 contract.
ALTER TABLE evidence_records
    ADD COLUMN scope_canonical JSONB NULL;

COMMENT ON COLUMN evidence_records.scope_canonical IS
    'Slice 474: canonical (sorted) wire scope the ingest content-hash was '
    'computed over, so the `atlas evidence verify` walk can reconstruct the '
    'exact record and recompute an identical hash. NULL = pre-slice-474 '
    'legacy row (verify reconstructs scope-free). Append-only; never '
    'mutated post-insert.';
