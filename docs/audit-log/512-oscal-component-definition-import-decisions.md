# Slice 512 — OSCAL component-definition import: JUDGMENT decisions log

**Slice:** 512 — OSCAL component-definition import (vendor control-implementation claims as evidence)
**Type:** JUDGMENT
**Parent:** 492 (OSCAL catalog import); sibling of 511 (OSCAL profile import)
**Author:** Claude (Engineer)
**Date:** 2026-06-07

This log records the subjective build-time calls per the JUDGMENT-slice
convention. The product-runtime AI-assist boundary is untouched (the bridge
imports no LLM; component-definition parsing is deterministic
compliance-trestle logic). The dominant constitutional concern for this slice
is the _fabricate-coverage_ boundary: a vendor's implemented-requirement is an
ASSERTION, and a naive auto-accept would fabricate control coverage that has
no platform-verified evidence behind it.

---

## Detection-tier classification (slice 353)

- `detection_tier_actual`: `none` — no latent bug surfaced during the slice.
  The trestle `ComponentDefinition` model + the no-dereference behavior were
  verified against real compliance-trestle 4.0.2 in the worktree venv before
  the Go code was written; the integration suite was green on the first full
  run against a real Postgres + real bridge.
- `detection_tier_target`: `integration` + `unit` — the no-auto-satisfy
  invariant (P0-512-1) is the load-bearing assertion and is covered at the
  integration tier (`TestImportComponentDefinition_DoesNotSatisfyAnyControl`
  asserts the import writes ZERO `control_evaluations` rows and ZERO
  `accepted` claims), backed by a schema-level CHECK
  (`imported_component_claims_is_vendor_claim_chk`) so the property holds even
  if a future caller is wrong. The no-external-deref guard (P0-512-2) is
  covered at the bridge unit tier (a monkeypatched socket that asserts no
  network call is attempted while parsing a document carrying external
  `link.href` / `source` values).

---

## D1 — Reuse the slice-492 provenance row + audit log (kind discriminator)

The import-run provenance + the append-only audit trail reuse the slice-492
`imported_catalogs` + `imported_catalog_audit_log` tables, extended via the
slice-511 `kind` discriminator (`'component_definition'`) + two new
audit-action values (`component_definition_imported` /
`component_definition_import_rejected`) + a new source value
(`oscal-component-import`). This keeps provenance (importer, source SHA-256,
vendor label, OSCAL version) and the immutable audit trail in ONE shared,
RLS-covered place across all three OSCAL ingest directions (catalog / profile
/ component-definition). The `imported_catalogs.control_count` carries the
TOTAL vendor-claim count for the import for display.

## D2 — A component-definition does NOT reuse `imported_catalog_controls`; new sibling tables (the load-bearing model call)

A catalog or a resolved profile is structurally a control SET, and slice 511
reused `imported_catalog_controls` for that. A component-definition is
structurally DIFFERENT: it is a vendor's set of CLAIMS, hierarchically
`component → control-implementation → implemented-requirement`. Forcing that
into `imported_catalog_controls` would (a) lose the component grouping, and
(b) — more importantly — blur the line between a control-set row and a vendor
CLAIM, which is exactly the distinction the dominant invariant (P0-512-1)
depends on. So slice 512 adds TWO new sibling tables:

- `imported_components` — one row per defined-component (uuid, type, title,
  description), FK → `imported_catalogs`.
- `imported_component_claims` — one row per implemented-requirement: the
  target `control_id`, the vendor's `statement`, the `requirement_uuid`, a
  NULLABLE `scf_anchor_id` (requirement → SCF anchor only — invariant #7),
  and two CLAIM-marking columns (`is_vendor_claim`, `claim_status`).

This is the hybrid the brief allowed ("mirror 511's `kind`-discriminator OR a
sibling shape per the spec"): shared provenance/audit (kind discriminator) +
sibling claim tables.

## D3 — A vendor claim is a CLAIM, never a satisfaction (P0-512-1, schema-enforced)

The persistence shape carries NO satisfied/active boolean an import could
flip. Two columns make the property unspoofable:

- `is_vendor_claim BOOLEAN NOT NULL DEFAULT TRUE` with a CHECK pinning it to
  `TRUE` — a row in this table is ALWAYS a vendor claim, never a
  platform-verified record.
- `claim_status TEXT NOT NULL DEFAULT 'asserted'` (CHECK
  `'asserted' | 'accepted' | 'rejected'`) — the IMPORT only ever writes
  `'asserted'`. Moving a claim to `'accepted'` is the EXISTING operator action
  (out of scope for this slice); the import never auto-accepts.

The integration test proves the import writes zero `control_evaluations` rows
(invariant #2 — ingestion never touches the evaluation surface) and zero
`'accepted'` claims. This keeps the import inside the CLAUDE.md
fabricate-coverage boundary even though no LLM is involved: the principle is
"no fabricated coverage," which a naive auto-accept of vendor claims would
violate.

## D4 — requirement → SCF anchor reconciliation reuses the slice-492 deterministic crosswalk

Each vendor claim's target `control_id` is matched against the current SCF
version's `scf_id` set (the same single-query crosswalk slice 492 / 511 use).
A claim whose target matches an SCF anchor gets `scf_anchor_id` set; an
unmatched claim is persisted with `scf_anchor_id = NULL` ("needs operator
mapping" — the import-unmapped-and-flag pattern). A claim is NEVER dropped for
being unmappable. The mapping is requirement → SCF anchor only — never
requirement → requirement (invariant #7 / P0-512-3). `scf_anchors` (the
bundled spine) is read-only and never written (P0-512-5).

## D5 — No external dereference; caps bound the expansion surface (threat-model I / D)

Unlike profile import (where trestle's `ProfileResolver` would actively fetch
an `import.href`), `ComponentDefinition(**doc)` is a pure pydantic parse — it
does not fetch anything. The bridge reads ONLY in-document prose
(requirement `description` + nested statement prose) via `_claim_statement`;
`links` / `source` / back-matter `href` values are opaque metadata, never
followed. The bridge unit test monkeypatches `socket.socket.connect` to raise
and parses a document carrying external `link.href` / `source` values — a pass
proves no network call was attempted (P0-512-2). Caps bound the
expansion-attack surface (threat-model D / AC-3): a 16 MiB byte cap (Go-side
before the wire + bridge-side), a 1,000-component cap, and a 50,000 total
vendor-claim cap.

## D6 — Statement flattening: requirement description + statement prose

A vendor's implementation narrative for a control can live in two OSCAL
places: the implemented-requirement's `description`, and the `description` /
parts-prose of any nested `statements`. `_claim_statement` concatenates both
(requirement description first, then each statement's prose) so the persisted
`statement` is the full vendor narrative for that control, with no `href`
followed. A requirement with no target `control-id` is skipped (it is unusable
as a claim) rather than fabricating a target.

## D7 — A zero-claim document is rejected, not imported empty

A component-definition with components but zero implemented-requirements
carries no vendor claims — there is nothing to surface as evidence. The bridge
rejects it (`valid=False`) rather than persisting an empty import, mirroring
the slice-492 "catalog contains zero controls" rejection. This keeps a
no-signal document from leaving a misleading provenance row.

---

## Anti-criteria verification (P0)

- **P0-512-1** (no auto-satisfy / fabricate coverage): claims land as
  `is_vendor_claim=TRUE, claim_status='asserted'`; CHECK-pinned; integration
  test proves zero `control_evaluations` + zero `accepted` rows. ✔
- **P0-512-2** (no external href dereference): pure pydantic parse; only
  in-document prose read; bridge socket-monkeypatch test. ✔
- **P0-512-3** (no requirement → requirement mapping): `scf_anchor_id` is a
  free-form SCF `scf_id`; there is no FK to another imported requirement. ✔
- **P0-512-4** (transactional, no partial persist): one transaction for the
  import + components + claims + success audit; a failure rolls back and
  writes only a rejection audit row in a separate tx; integration test. ✔
- **P0-512-5** (no SCF-spine / imported-catalog overwrite): `scf_anchors` is
  read-only; the new tables are NEW; the ALTERs only widen CHECKs. ✔
- **P0-512-6** (no anonymous / cross-tenant import): `grc_engineer` / `admin`
  role gate + FORCE RLS four-policy on both new tables; tenant-isolation
  integration test. ✔
- **P0-512-7** (no second bridge process): the existing `oscal-bridge` is
  extended with one new RPC; no new process. ✔
