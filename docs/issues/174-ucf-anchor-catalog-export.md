# 174 — UCF anchor catalog export (nested / two-sheet)

**Cluster:** Backend / Frontend
**Estimate:** 1-2d
**Type:** AFK (D1 locked 2026-05-20 by maintainer — ship all three formats, each with its native projection)
**Status:** `ready` (demand gate cleared 2026-05-20; D1 locked — see "What this slice ships" below)

## Narrative

Spillover from slice 137 — at D1 the slice 137 engineer rejected
Option B (nested) and Option C (two-sheet XLSX) for the controls
UCF graph export and shipped Option A (flat). The rejected options
remain valid for a DIFFERENT entity: the SCF anchor catalog itself.

This slice ships `GET /v1/anchors/export?format=<csv|json|xlsx>`
that exports the SCF anchor catalog (anchor metadata + framework
satisfactions + STRM edges + anchor → framework requirement
crosswalk). Unlike the slice 137 controls export, the anchor IS
the row here, and the nested-vs-two-sheet question becomes the
load-bearing JUDGMENT call.

**What this slice ships:** `GET /v1/anchors/export?format=<csv|json|xlsx>` that
exports SCF anchors with their framework satisfactions attached. **D1 locked
2026-05-20 by maintainer — each format gets its native projection (ship all three):**

- `format=json` → **nested JSON.** One JSON object per anchor; framework satisfactions
  live in a `framework_satisfactions` array field on the anchor object. Programmatic
  consumers consume the graph in its natural shape.
- `format=xlsx` → **two-sheet workbook.** Sheet 1 = Anchors (one row per anchor, anchor
  metadata only); Sheet 2 = Edges (one row per anchor → framework-requirement edge,
  pivot-table-friendly). Auditors get the artifact in the format they actually open
  (Excel). VLOOKUP / XLOOKUP across sheets is the canonical join.
- `format=csv` → **flat-nested fallback.** One row per anchor; framework satisfactions
  serialized as a JSON-string column. Grep / awk friendly; the single-file constraint
  is honored even though the projection is lossy compared to XLSX.

Locking all three rather than picking one preserves the "right tool per consumer"
property — auditors → XLSX, programmers → JSON, command-line crowd → CSV. The cost
of supporting all three is small because the export library (slice 135) already
handles format-dispatch; the only new work is the two-sheet writer + the nested
projection shape.

**Scope discipline (what is OUT):** the SCF anchor catalog itself
(not tenant data) — so the slice's threat model is about export
DoS, not information disclosure. `applicability_expr` is NOT
exported here (that's tenant-private and lives in slice 137).

## Threat model

Inherits slice 135. Anchor-catalog-specific:

| STRIDE     | Concern                                                                  | Mitigation                                                                    |
| ---------- | ------------------------------------------------------------------------ | ----------------------------------------------------------------------------- |
| **D** DoS  | The SCF catalog is ~1,400 anchors × multiple satisfactions; fan-out high | Same slice 135 / 145 cap. Anchor catalog row cap: 100K (well above realistic) |
| **I** Info | Anchor catalog is public — minimal disclosure concern                    | No tenant-private fields to leak; STRM edges are public-domain crosswalks     |

## Acceptance criteria (stub — expand at pickup)

- [ ] AC-1: `GET /v1/anchors/export?format=...` reuses slice 135 library.
- [ ] AC-2: All three formats ship per the maintainer-locked D1 (json = nested,
      xlsx = two-sheet, csv = flat-nested fallback). Decisions log at
      `docs/audit-log/174-ucf-anchor-catalog-export-decisions.md` records the
      maintainer pre-lock + any impl-time refinements.
- [ ] AC-3: BFF route + Export button on the anchors / UCF browse page.
- [ ] AC-4: Cross-tenant isolation N/A (catalog is global); test asserts the
      export is identical across tenants.
- [ ] AC-5: OPA admit-set parity test.
- [ ] AC-6: Meta-audit row (action = `anchors_export`).
- [ ] AC-7: Streaming-memory test asserts under 200 MB.
- [ ] AC-8: CHANGELOG entry.

## Constitutional invariants honored

Inherits slice 135. Adds explicit support for invariant #1
("one control, N framework satisfactions") — the export carries the
satisfactions inline so consumers see the full graph in one file.

## Dependencies

- **#135** Data-export library. **Gate: 135 merged.** (Already merged.)
- **Customer demand gate — CLEARED 2026-05-20 by maintainer.** The "wait for an
  operator to ask" gate was original-design conservatism. Maintainer green-lit
  on the grounds that (a) the catalog is public-domain data, so there's no
  tenant-leak risk and the threat surface is just export-DoS (already
  capped by slice 145), (b) the cost is small (~1-2d) because slice 135's
  export library does most of the heavy lifting, and (c) shipping the catalog
  export rounds out the "this self-host product feels finished" story for
  vendor-due-diligence packs and auditor-handoff bundles.

## Anti-criteria (P0 — block merge)

- Inherits slice 135 P0-A1 through P0-A14.
- **P0-A-174-1:** The export MUST NOT include any tenant-private
  data — `applicability_expr` is excluded; only the SCF catalog +
  tenant-agnostic crosswalk metadata.
- **P0-A-174-2:** Two-sheet XLSX (if D1 picks it) MUST still satisfy
  slice 135 P0-A6 — TWO sheets allowed (anchors + edges); NO chart
  objects, NO named ranges, NO VBA.

## Skill mix

- slice 135's `internal/export/` library — consume only.
- Go integration tests + Playwright e2e.
- XLSX library / handcrafted writer extension (D1 dependent).

## Notes for the implementing agent

This slice's D1 picks up exactly where slice 137 D1 ended.
Slice 137 D1 documented the rejected alternatives (B nested + C
two-sheet) with full reasoning; this slice's D1 should re-read
those rejections and decide whether the cost-benefit shifts when
the entity changes from tenant-control to global-anchor.

Provenance: filed 2026-05-19 as a spillover of slice 137 D1.
