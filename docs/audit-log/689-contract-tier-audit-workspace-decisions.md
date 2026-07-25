# Slice 689 — contract-tier audit-workspace read tail — decisions log

JUDGMENT slice. The build-time subjective calls (which remaining audit-workspace
tail routes to cover vs. defer, the per-route seam shape, the golden variants,
and the passthrough-vs-field-contract consumer-assert disposition) are recorded
here per the continuous-batch JUDGMENT convention; the maintainer iterates
post-deployment. This does NOT touch the product-runtime AI-assist boundary
(separate, constitutional).

Cross-references: ADR-0007 (`docs/adr/0007-contract-test-tier.md`), slice 687
decisions log (`docs/audit-log/687-contract-tier-tail-remaining-decisions.md`,
D3 no-BFF field-contract disposition + D4 route scoping — this slice drains the
tail 687 deferred), slice 412 decisions log D5
(`docs/audit-log/412-contract-tier-tail-decisions.md`), slice 411 decisions log
(`docs/audit-log/411-contract-tier-controls-audit-decisions.md`, the per-route
Option-A seam + recorder + transform-aware-vs-passthrough-assert pattern this
slice mirrors), slice 689 spec
(`docs/issues/689-contract-tier-audit-workspace-read-tail.md`).

- detection_tier_actual: contract
- detection_tier_target: contract

---

## D1 — Route scope: cover the four single-resource reads that have a real verbatim-passthrough GET BFF; defer the rest (AC-2)

The slice-687 deferred tail is large (the slice file lists populations/samples,
walkthroughs, audit-notes, the attestation routes, and the controldetail
Evidence ledger window). Per the slice-411 D1 / slice-412 D1 / slice-687 D4
precedent (a clean bounded cut + a spillover beats an overreaching slice) and
the slice file's own "prioritize routes with a real verbatim-passthrough GET BFF
and the ones the `/e2e/` suite still hand-mocks," I cut to the four
single-resource reads that each have a SHIPPED verbatim-passthrough GET BFF:

| Route                        | Package        | BFF (verbatim passthrough)                      | Consumer half        |
| ---------------------------- | -------------- | ----------------------------------------------- | -------------------- |
| `GET /v1/populations/{id}`   | `audit`        | `web/app/api/audit/populations/[id]/route.ts`   | full BFF passthrough |
| `GET /v1/samples/{id}`       | `audit`        | `web/app/api/audit/samples/[id]/route.ts`       | full BFF passthrough |
| `GET /v1/walkthroughs/{id}`  | `walkthroughs` | `web/app/api/audit/walkthroughs/[id]/route.ts`  | full BFF passthrough |
| `GET /v1/audit-notes/thread` | `auditnotes`   | `web/app/api/audit/audit-notes/thread/route.ts` | full BFF passthrough |

All four are the load-bearing audit-workspace reads: each is driven by a real
Next.js BFF that forwards the upstream body verbatim (`forwardJSON` → upstream
`.text()` passthrough), so all four get the FULL consumer treatment (provider
record + BFF passthrough drive + the populations drift proof, AC-3) — none fall
to the slice-687-D3 field-contract-only disposition, because in every case a
consumer BFF exists today.

Confidence: **high.**

## D2 — The slice file's "GET /v1/populations (list) + GET /v1/samples (list)" is a SPEC MISCHARACTERIZATION; the real read surface is the single-resource GET (grill-with-docs finding)

The slice file (and slice 687 D4) name "`GET /v1/populations` (list) + `GET
/v1/samples` (list)" in `internal/api/audit`, with the note "the
populations/samples BFFs mix POST (create) with GET (list); inventory the READ
subset first." Grilling the actual package surfaced a drift the spec did not
anticipate:

**There are NO list-GET routes in `internal/api/audit`.** The package serves
(`internal/api/audit/handlers.go` package doc + handlers):

- `POST /v1/populations` (create) · `GET /v1/populations/{id}` (single)
- `POST /v1/samples` (draw) · `GET /v1/samples/{id}` (single)
- `POST /v1/samples/{id}/annotations` · `GET /v1/samples/{id}/annotations`

The bare `web/app/api/audit/populations/route.ts` + `.../samples/route.ts` BFFs
are POST-only (create/draw). The only verbatim-passthrough GET BFFs are the
`[id]` single-resource reads (`populations/[id]/route.ts`, `samples/[id]/
route.ts`). So the "list GET subset" the spec asked me to inventory does not
exist — the correct read surface to pin is the **single-resource GET**, which I
covered. I did NOT invent a list route to match the spec wording; I covered the
real read surface and recorded the mischaracterization here. The
`GET /v1/samples/{id}/annotations` list read is a SEPARATE route with no
verbatim GET BFF (it is read by the sample-detail component, not a thin
passthrough) — deferred + spilled (D4).

Confidence: **high** (the absence is verified against the package source, not
assumed).

## D3 — Per-route Option-A seams, sized to exactly the methods each route calls (AC-1)

Each handler reads through a narrow unexported seam, sized per the slice-412 D2
sizing rule (just the methods the targeted routes call), wired by the unchanged
public `New(...)` to the same concrete store (P0-409-2 — only the private struct
shape changes):

- **`audit`** — a two-method `sampleReader` (`GetPopulation` + `GetSample`).
  The write/draw/annotate handlers keep using the concrete `h.store`.
- **`walkthroughs`** — a one-method `walkthroughReader` (`Get`). The
  create/list/attach/finalize handlers keep `h.store`. `Export` ALSO calls
  `Get`, but it streams a download envelope (a JSON or PDF attachment), not the
  `{walkthrough}` wire shape the BFF consumes — it is left on the concrete
  store so the seam stays a one-method read surface (the BFF-consumed read is
  `Get`, not `Export`).
- **`auditnotes`** — a one-method `threadReader` (`ListThreadForScope`). The
  `Create` + legacy author-scoped `List` handlers keep `h.store`. The `/thread`
  route is the one with the shipped verbatim-passthrough BFF, so it is the
  load-bearing read this slice pins.

Each recorder injects a fixed-row stub via an unexported
`newHandlerWithReader`, so the wire shapes record on the plain `go test ./...`
unit surface with NO Postgres pool (ADR-0007 / P0-409-1 — no recorder on the
integration surface).

Confidence: **high.**

## D4 — Deferred + spilled (slice 690)

Deferred (each carries its own seam/auth/pagination cost or has no verbatim GET
BFF; folding them in would overreach a coherent cut):

- **`GET /v1/samples/{id}/annotations`** (the annotation list read; no verbatim
  passthrough BFF today — read by the sample-detail component).
- **`GET /v1/walkthroughs` (list)** — the list BFF
  (`web/app/api/audit/walkthroughs/route.ts`) is POST-only today; no GET
  consumer exists, so per slice-687 D3 it would be a field-contract pin, not a
  passthrough drive. Deferred until a list GET BFF lands.
- **`GET /v1/audit-notes` (legacy author-scoped list)** — no GET BFF (the
  workspace reads the `/thread` route, not the legacy list).
- **`GET /v1/controls/{id}/attestations` / `attest-form`** — the attestation
  handler (`internal/api/controls/attest.go`); lower e2e traffic, and the
  `attest-form` route assembles a schema descriptor (its own seam shape).
- **`GET /v1/evidence?control_id=…`** — the controldetail per-control Evidence
  ledger window; left on the concrete `*Store` by slices 411/412 (its own
  keyset-pagination + `CountEvidenceForTenant` two-method seam), and it is not
  part of the control-detail tab cluster the e2e suite traverses.
- **The audit-period passthrough half** (slice 687 D3 / spillover) — when a
  single-period or audit-sampling BFF lands that consumes
  `GET /v1/audit-periods/{id}` or `/control-state`, add the passthrough-drive
  consumer half to slice 687's field-contract goldens.

All carried into spillover **slice 690**
(`docs/issues/690-contract-tier-audit-workspace-read-tail-remainder.md`).

Confidence: **high.**

## D5 — Zero-new-gate (AC-4) + coverage

No new CI job, no new gate, no new tool, no new dependency (ADR-0007 (d)). The
three recorders ride the existing `Go · build + test` unit surface; the four
consumers ride `Frontend · vitest` (auto-enrolled by slice 348's `**/*.test.ts`
directory walk). No `ci.yml` change. `go build ./...`, `go vet`, and
`golangci-lint run` on the three changed packages are clean (0 issues); no
`go.mod`/`go.sum` diff (chi / uuid / pgx / audit / walkthrough / notes / authctx
/ credstore / tenancy are all pre-existing).

Coverage: `internal/api/audit`, `internal/api/walkthroughs`, and
`internal/api/auditnotes` are HTTP/RLS handler packages integration-tested
against real Postgres (on the coverage-gate exclude list). The seam refactors
add no uncovered production branch — each `reader` field points at the same
store, so the Get/Thread bodies changed `h.store.X` → `h.reader.X` /
`h.reader.Get`, identical coverage. No per-package floor to lift; no ratchet
obligation.

## D6 — Drift-sensitivity proof (AC-3)

Proved on the `GET /v1/populations/{id}` endpoint. Renamed the golden's
`row_count` key → `row_total`. Result:

- **Provider half failed** (`go test ./internal/api/audit/ -run
TestContract_PopulationGet`): both the `open` and `frozen` variants reported
  `wire shape drifted from golden` — the handler emits `row_count`, the golden
  now said `row_total`.
- **Consumer half failed** (`npm run test -- population-get.contract`):
  `frozen.population.row_count: expected 'undefined' to be 'number'` — the field
  contract assertion.

Both halves red on a single-field rename; golden restored; both green. This is
the slice-210-class catch reproduced on the audit-workspace single-population
read — the exact bug class ADR-0007 exists to catch.

---

## Revisit once in use

- **The deferred audit-workspace read tail.** The sample-annotation list read,
  the walkthroughs + legacy audit-notes list reads, the attestation routes, and
  the controldetail per-control Evidence window remain unguarded. Spilled
  (slice 690).
- **The list-GET surface, if it lands.** When a walkthroughs LIST GET BFF or an
  audit-notes LIST GET BFF lands, the deferred list reads gain a passthrough
  drive (currently they would be field-contract pins per slice 687 D3).
- **`canonical_hash` encoding.** The walkthrough golden records the hex-encoded
  digest (64 lowercase hex chars). If a future export changes the encoding, the
  golden + the consumer's `/^[0-9a-f]{64}$/` assert update together.
- **`created_at` millisecond formatting.** The audit-notes golden records
  `created_at`/`updated_at` in the `2006-01-02T15:04:05.000Z` (millisecond)
  format `noteWireFrom` emits — distinct from the RFC3339Nano the other
  audit-workspace wires use. If the note wire format is normalized, the golden
  re-records.

## Confidence

| Decision                                                       | Confidence |
| -------------------------------------------------------------- | ---------- |
| D1 — cover the four verbatim-passthrough single-resource reads | high       |
| D2 — populations/samples "list" is a spec mischaracterization  | high       |
| D3 — per-route Option-A seams sized to the called methods      | high       |
| D4 — defer the remaining tail; spill to slice 690              | high       |
| D6 — drift sensitivity proven on the populations endpoint      | high       |

## Spillovers

- **slice 690** — contract-tier rollout: audit-workspace read-tail remainder
  (the `GET /v1/samples/{id}/annotations` list read; the walkthroughs +
  legacy audit-notes list reads; the attestation routes in
  `internal/api/controls/attest.go`; the controldetail per-control `Evidence`
  ledger window; the audit-period passthrough half). `ready` — deps (the
  slice-411/412/687/689 per-route seam pattern) are on `main`/this PR.
  `docs/issues/690-contract-tier-audit-workspace-read-tail-remainder.md`.
