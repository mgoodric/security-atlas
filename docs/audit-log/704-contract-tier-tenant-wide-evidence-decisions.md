# Slice 704 — decisions log (contract-tier tenant-wide `/v1/evidence` ledger window)

- detection_tier_actual: none
- detection_tier_target: none

JUDGMENT slice. No bug surfaced during the slice — the work is a pure
contract-tier extension (add a tenant-wide recorder + golden + consumer assert)
over the seam slice 692 already shipped. The provider recorder + drift proof are
the tests; both passed first-build after `-update` generated the golden.

## Decisions made

### D1 — Extend the EXISTING `evidenceWindowReader` seam, not a sibling seam

The slice text left the seam shape to implementer JUDGMENT: "Extend the
slice-692 `evidenceWindowReader` seam (or add a sibling `evidenceTenantWideReader`
seam — implementer's JUDGMENT) with `EvidencePaged`."

- **Options considered.** (a) Add `EvidencePaged` to the existing
  `evidenceWindowReader` interface. (b) Define a new
  `evidenceTenantWideReader` interface carrying `EvidencePaged` +
  `CountEvidenceForTenant`, give the Handler a fourth field, wire a third
  `newHandlerWith…` constructor.
- **Chosen: (a).** The `Evidence` handler is ONE handler reading ONE ledger
  table (`evidence_records`) through two shaped sqlc queries
  (`ListEvidenceForControlPaged` / `ListEvidencePaged`) plus the shared
  `CountEvidenceForTenant`. A single seam keeps the Handler struct at one
  evidence field, reuses the existing `newHandlerWithEvidenceReader`
  constructor, and lets the existing `stubEvidenceReader` grow one method + one
  fixture field rather than spawning a parallel stub type. The production
  `*Store` already implements `EvidencePaged` verbatim, so satisfying the
  widened interface is free (no production code change beyond routing the two
  reads through `h.evidence`).
- **Rationale.** Sibling seams are warranted when two surfaces have genuinely
  distinct read shapes or distinct lifecycles. Here the two branches share the
  count method and the same envelope/wire shape; splitting would duplicate the
  `CountEvidenceForTenant` requirement across two interfaces for no isolation
  benefit. Altitude: widen the existing mechanism, don't special-case.
- **Confidence: high.**

### D2 — Filter-matrix coverage = pin the wire once + exercise the request surface, not one variant per filter

The tenant-wide branch carries a six-predicate optional-filter matrix (`kind`,
`result`, `source_actor_type`, `source_actor_id`, `scope_cell_id`, plus the
`[since, until]` window). The contract tier pins the WIRE SHAPE the BFF and
frontend bind to — not the SQL `WHERE`-clause behavior (that is the integration
tier's job, exercised by the slice-106 / slice-234 integration suites).

- **Key observation.** The optional filters NARROW the result rows; they do not
  change the envelope or row wire shape. `kind=X` returns fewer rows, each with
  the identical `evidence_wire` shape. So one variant per filter would record
  six byte-identical envelopes — no additional contract signal.
- **Options considered.** (a) One golden variant per filter predicate (6+
  variants). (b) Pin the envelope + row shape with a `populated` variant, pin
  the filter-matrix REQUEST surface with one `filtered` variant whose request
  line carries a non-empty `kind + result + scope_cell_id + source_actor_* +
since/until` predicate (so the handler parses every filter param on the path
  to the happy-path read), and pin the `count 0 / total > 0` disambiguation
  with an `empty` variant.
- **Chosen: (b) — three variants.** `populated` (unfiltered, fully-populated +
  fully-nulled rows — pins every nullable field's present/absent branch),
  `filtered` (non-empty filter matrix on the request line — pins that a
  filtered request still produces the canonical envelope and that the handler
  parses each filter param without erroring), `empty` (zero rows, non-zero
  tenant-wide total — pins slice 236's "filters narrowed to zero, ledger not
  empty" disambiguation).
- **Rationale.** This mirrors the slice-692 per-control recorder's
  two-variant (`populated` / `empty`) discipline and adds exactly the one
  variant the tenant-wide branch needs that the per-control branch lacks: the
  filter-matrix request surface. No per-filter combinatorial explosion that
  records duplicate wire bytes.
- **Confidence: high.**

### D3 — Consumer disposition = `toEqual` verbatim passthrough (confirmed at pickup)

The slice flagged "Confirm at pickup" on the BFF disposition.

- **Confirmed.** `web/app/api/evidence/route.ts` reads `upstream.text()` and
  returns `new NextResponse(body, { status: upstream.status, … })` — it forwards
  the upstream body bytes + status unchanged, whitelisting only which query
  params it forwards. No body transform. So the consumer half is
  `toEqual(golden)` (same disposition as the slice-692 per-control branch and
  the slice-411 control-detail tabs), NOT a transform-aware or field-contract
  assert (slice 687 D3).
- **Confidence: high.**

### D4 — `filtered` variant's request line uses neutral synthetic strings (GitGuardian)

The `filtered` variant's request line carries `kind=access_review.completion.v1`,
`source_actor_type=service`, `source_actor_id=okta-sync`, `scope_cell_id=<the
synthetic contract scope UUID>`. The `evidence_kind` values
(`sast.scan_result.v1`, `access_review.completion.v1`) are the canonical
`schemas/` evidence-kind identifiers; the connector/runner provenance blobs
(`github`/`ci-runner`, `okta`/`scheduled-poll`) are generic vendor nouns, not
credentials. No JWT-shaped or token-shaped literals (slice 314 / GitGuardian).

- **Confidence: high.**

## Revisit once in use

- **`filtered` variant fidelity.** The contract tier deliberately does NOT
  assert the filter SQL actually narrowed the rows (the stub returns fixed rows
  regardless of the request predicate). If a future refactor moves filter
  application from the SQL query into the handler/wire layer, this recorder
  would NOT catch a filter that silently stopped narrowing — that remains the
  integration tier's job (the slice-106 / slice-234 integration suites). Re-check
  that those integration suites stay enrolled when the `/evidence` list view
  gains real filter-pill churn.
- **`next_cursor` non-empty path.** Both evidence recorders (692 + 704) record
  only the single-page case (`next_cursor: ""`). The keyset-cursor wire token
  itself is never pinned by a contract golden. If the cursor encoding changes
  (`pagination.go` `encodeCursor`), no contract test catches the wire-token
  shape. Consider a follow-on variant that returns `pageRows+1` rows so
  `next_cursor` is a non-empty base64url token — applies to BOTH the per-control
  (692) and tenant-wide (704) branches. Captured below as spillover candidate;
  filed if the cursor encoding is touched.
- **Source-actor filter wire.** `source_actor_type` / `source_actor_id` filter
  on the JSONB `source_attribution` column; they are exercised on the REQUEST
  line of the `filtered` variant but the recorded rows' `source` blobs use the
  `provenance` column, not `source_attribution`. The wire `source` field is the
  provenance blob (per `evidenceWireFromListRow`), so this is correct — but
  re-confirm the provenance-vs-attribution distinction holds if the wire ever
  surfaces `source_attribution` directly.

## Confidence summary

| Decision                                  | Confidence |
| ----------------------------------------- | ---------- |
| D1 — extend existing seam                 | high       |
| D2 — three-variant filter-matrix coverage | high       |
| D3 — toEqual passthrough consumer         | high       |
| D4 — neutral synthetic fixture strings    | high       |

All decisions high-confidence: this slice is a faithful extension of the
slice-692 pattern over a branch whose wire shape was already established by
slices 106 / 234 / 236. The only genuine deferrals (non-empty `next_cursor`
wire token; integration-tier filter-narrowing fidelity) are recorded above as
revisit items, not blockers.
