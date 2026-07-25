# Slice 692 — contract-tier controldetail attest-form + per-control Evidence window — decisions log

JUDGMENT slice. The build-time subjective calls (the two per-route seam shapes
over Postgres-coupled `dbx`-row types, the golden variants, the
passthrough-vs-field-contract consumer-assert disposition, and the per-control
vs tenant-wide Evidence-window seam scope) are recorded here per the
continuous-batch JUDGMENT convention; the maintainer iterates post-deployment.
This does NOT touch the product-runtime AI-assist boundary (separate,
constitutional).

Cross-references: ADR-0007 (`docs/adr/0007-contract-test-tier.md`), slice 690
decisions log (`docs/audit-log/690-contract-tier-audit-remainder-decisions.md`,
D3 — the two dbx-coupled control-detail deferrals this slice drains), slice 689
decisions log (`docs/audit-log/689-contract-tier-audit-workspace-decisions.md`,
D3 per-route seam shape), slice 687 decisions log
(`docs/audit-log/687-contract-tier-tail-remaining-decisions.md`, D3 no-BFF
field-contract disposition), slice 412 decisions log
(`docs/audit-log/412-contract-tier-tail-decisions.md`), slice 411 decisions log
(`docs/audit-log/411-contract-tier-controls-audit-decisions.md`, the per-route
Option-A seam + recorder + passthrough-vs-field-contract pattern this slice
mirrors), slice 692 spec
(`docs/issues/692-contract-tier-controldetail-attest-form-evidence-window.md`).

- detection_tier_actual: contract
- detection_tier_target: contract

---

## D1 — Two per-route Option-A read seams over the dbx-row types (AC-1)

Slice 690 deferred these two reads because each carries its own heavier seam
shape over a Postgres-coupled `dbx`-row type (not the clean domain structs the
audit-workspace handlers return). I built each as an Option-A per-route read
seam (slice 411/689 precedent — the narrow interface the handler depends on,
satisfied verbatim by the production `*Store` / pool and by a fixed-row
recorder stub with no Postgres):

| Route                               | Package         | New seam                          | Seam methods                                                                                             |
| ----------------------------------- | --------------- | --------------------------------- | -------------------------------------------------------------------------------------------------------- |
| `GET /v1/controls/{id}/attest-form` | `controls`      | `controlByIDReader` (1-method)    | `ControlByID(ctx, tenantID, id) → dbx.GetControlByIDRow`                                                 |
| `GET /v1/evidence?control_id=…`     | `controldetail` | `evidenceWindowReader` (2-method) | `EvidenceForControl(...) → []dbx.ListEvidenceForControlPagedRow` + `CountEvidenceForTenant(ctx) → int64` |

Both public constructors are byte-unchanged in signature (P0-409-2):
`NewAttestHandler(pool, ingester, uploader)` and `controldetail.New(store)`. The
recorder injects via unexported `newAttestHandlerWithReader` /
`newHandlerWithEvidenceReader`. No DB, no integration tag (P0-409-1).

### D1a — `AttestHandler` pool-nil → 503 contract preserved verbatim

Refactoring `attest.go`'s read off the concrete pool required care: the prior
`h.pool == nil → 503` guard (the unit-only servers that exercise the pre-read
401/400 branches) had to survive. Resolution: `NewAttestHandler` wires
`reader = poolControlReader{pool}` ONLY when `pool != nil`; a nil pool leaves
`reader` nil and the read-path gate now checks `h.reader == nil → 503`. This is
behaviour-identical to the old guard for every existing call site (the unit
tests construct `NewAttestHandler(nil, …)` and exit at the 401/400 gate before
the read; the integration tests pass a real pool). The now-dead
`AttestHandler.pool` field was removed (the read moved to
`poolControlReader.pool`). The former `h.loadControl` method body moved verbatim
into `poolControlReader.ControlByID`.

## D2 — Consumer disposition: toEqual BFF-drive for BOTH routes (NOT field-contract)

Slice 690's two deferrals were flagged as candidate field-contract pins (slice
687 D3 — no verbatim-passthrough BFF). On inspection BOTH routes DO have a
verbatim-passthrough GET BFF today, so the stronger toEqual BFF-drive
disposition applies (like slice 411's control-detail tabs, not slice 690's
field-contract annotation list):

- `web/app/api/controls/[id]/attest-form/route.ts` — `getAttestForm`
  (`web/lib/api/attest.ts`) returns `res.json()` unchanged; the route does
  `NextResponse.json(form)`. Verbatim passthrough.
- `web/app/api/evidence/route.ts` — forwards the upstream body bytes + status
  unchanged (`new NextResponse(body, {status})`). Verbatim passthrough.

Each consumer half therefore (a) field-contract-pins the load-bearing
wire-shape assumptions AND (b) drives the BFF with each provider variant and
asserts `toEqual(golden)`. The toEqual drive is the stronger assertion — it
fails on ANY passthrough regression, not just the pinned fields.

## D3 — JUDGMENT: Evidence seam scoped to the PER-CONTROL branch only; tenant-wide branch deferred + spilled

The spec explicitly left this as the JUDGMENT call. The `Evidence` handler
serves two branches off the same entry point: the per-control window
(`?control_id=…`, slice 064) and the tenant-wide ledger window (no
`control_id`, slice 106 + the six optional filters + slice 234 scope_cell).

I seamed **only the per-control branch**. Rationale:

1. The `/e2e/` suite traverses the control-detail surface's per-control evidence
   card; it does NOT traverse the tenant-wide `/evidence` list view from this
   surface. The contract-tier's job is to pin the wire the e2e suite depends on.
2. The tenant-wide branch is its own filter-heavy recorder story
   (`EvidencePaged` + six optional filter predicates + a distinct
   `dbx.ListEvidencePagedRow` row type). Recording it well means exercising the
   filter matrix, which is a materially bigger fixture than the per-control
   window. A clean coherent cut + a spillover beats an overreaching slice
   (slice 411 D1 / 412 D1 / 687 D4 / 689 D1 / 690 D1 precedent).

The tenant-wide branch keeps using `h.store` directly; the per-control branch's
`total` (the slice-236 tenant-wide ledger count) is read through the same
two-method seam (`CountEvidenceForTenant`) so the per-control envelope records
fully with no Postgres. **Deferred to slice 704** (spillover).

## D4 — Golden variants pin the full nullable matrix + both gate branches

- **attest-form** — two 200 variants: `owner` (credential holds the control's
  `owner_role` → `caller_can_attest: true`) and `viewer` (a control-read
  credential that does NOT hold it → `caller_can_attest: false`). Both branches
  of the gate that drives the frontend attest button's enabled state are
  pinned. The non-manual-implementation **400** branch is an error envelope
  (not a form body), so it is pinned by a sibling unit test
  (`TestAttestForm_NonManual400`), not a golden variant. `manual_evidence_schema`
  is recorded as a real JSON Schema fragment so the JSONB decode + passthrough
  path is exercised.
- **per-control evidence** — `populated` (two rows: one fully populated, one
  fully nulled — `evidence_kind` null, `scope_cell` null, `source` null) and
  `empty` (zero rows but `total: 3`, pinning the slice-236 "filters narrowed to
  zero, ledger not empty" disambiguation: `count: 0, total > 0`). The
  populated variant pins `total (7) ≠ count (2)` so a future refactor that
  accidentally returns the page length as `total` is caught.

## D5 — AC-3 drift sensitivity proven

Mutating `AttestationVersion` (`1.1.0` → a sentinel) made the
`TestContract_AttestForm` recorder fail on both variants; reverting restored
green. The recorder's compare-or-`-update` core (`assertContractGolden`) is the
drift gate for both new endpoints.

## D6 — AC-4 zero-new-gate

No `ci.yml` change. The two Go recorders ride the existing `Go · build + test`
unit surface (`go test ./...`); the two consumer specs ride the existing
`Frontend · vitest` surface. No fifth CI job (ADR-0007 (d)).

---

## Spillover

- **Slice 693** — contract-tier coverage for the tenant-wide `/v1/evidence`
  ledger window (the slice-106 + slice-234 filter matrix branch the slice-692
  seam deliberately excluded). `ready` (the per-route seam pattern + the
  `controldetail` recorder scaffolding land with this slice).
