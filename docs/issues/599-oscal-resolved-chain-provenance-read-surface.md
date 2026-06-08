# 599 — OSCAL resolved-chain provenance read surface

**Cluster:** evidence-pipeline (OSCAL)
**Estimate:** S (<1d)
**Type:** standard
**Status:** `ready`
**Parent:** #578 (OSCAL chained profile-over-profile resolution)

## Narrative

Slice #578 records the resolved import chain as provenance in the
`imported_catalog_audit_log.detail` JSON of the `profile_imported` success row:
a `chain` array of `{role, sha256, bytes}` entries (entry profile +
intermediate profiles + catalogs) plus a `chain_depth` count. The provenance is
WRITTEN but there is no read surface that surfaces it to an operator/auditor —
it is only queryable by reading the raw audit-log JSON.

This slice adds a read path so an operator can see, for an imported profile
baseline, the exact chain of documents (and their hashes) that resolved it —
the "diligence the diligence tool" provenance story for chained imports.

## Scope

- A query (sqlc) + handler that, given an imported profile baseline id, returns
  the resolved-chain provenance from its `profile_imported` audit row.
- Surface it in the imported-baseline detail view (web) alongside the existing
  control list, or as a CLI `--show-chain` flag on a baseline read command —
  pick one in the slice's decisions log.

## Acceptance criteria (outline)

- **AC-1.** Given a chained import's baseline id, the chain provenance (each
  document's role + sha256) is returned in document order.
- **AC-2.** A single-level (slice-511) baseline returns its two-element chain
  (entry-profile + catalog) without special-casing.
- **AC-3.** RLS-scoped: a tenant only sees its own baselines' provenance.

## Dependencies

- **#578** (chained profile resolution) — writes the provenance this slice
  reads. Merged.

## Skill mix

`tdd` · `simplify`.
