# Slice 690 — contract-tier audit-workspace read-tail remainder — decisions log

JUDGMENT slice. The build-time subjective calls (which remaining read-tail
routes to cover vs. defer, the per-route seam shape, the golden variants, and
the passthrough-vs-field-contract consumer-assert disposition) are recorded here
per the continuous-batch JUDGMENT convention; the maintainer iterates
post-deployment. This does NOT touch the product-runtime AI-assist boundary
(separate, constitutional).

Cross-references: ADR-0007 (`docs/adr/0007-contract-test-tier.md`), slice 689
decisions log (`docs/audit-log/689-contract-tier-audit-workspace-decisions.md`,
D3 per-route seam shape + D4 the deferred tail this slice drains), slice 687
decisions log (`docs/audit-log/687-contract-tier-tail-remaining-decisions.md`,
D3 no-BFF field-contract disposition), slice 412 decisions log D5
(`docs/audit-log/412-contract-tier-tail-decisions.md`), slice 411 decisions log
(`docs/audit-log/411-contract-tier-controls-audit-decisions.md`, the per-route
Option-A seam + recorder + transform-aware-vs-passthrough-assert pattern this
slice mirrors), slice 690 spec
(`docs/issues/690-contract-tier-audit-workspace-read-tail-remainder.md`).

- detection_tier_actual: contract
- detection_tier_target: contract

---

## D1 — Route scope: cover the three audit-workspace LIST reads that extend an existing 689 seam and are pure-Go to record; defer the two dbx-coupled control-detail reads (AC-2)

The slice-690 spec lists five candidate routes. Per the slice-411 D1 / slice-412
D1 / slice-687 D4 / slice-689 D1 precedent (a clean coherent cut + a spillover
beats an overreaching slice), I cut on a sharp, defensible seam: **the three
audit-workspace LIST reads that each extend an existing slice-689 per-route seam
by one method and return clean domain structs** (pure-Go to record, no
Postgres-row fixture cost):

| Route                               | Package        | Seam extension (from 689)                  | Consumer half       |
| ----------------------------------- | -------------- | ------------------------------------------ | ------------------- |
| `GET /v1/samples/{id}/annotations`  | `audit`        | `sampleReader` += `ListAnnotations`        | field-contract (D3) |
| `GET /v1/walkthroughs` (list)       | `walkthroughs` | `walkthroughReader` += `List`              | field-contract (D3) |
| `GET /v1/audit-notes` (legacy list) | `auditnotes`   | `threadReader` += `ListForAuthorAndPeriod` | field-contract (D3) |

All three:

- extend a seam slice 689 already introduced (one added method each — the
  slice-412 D2 sizing rule: the seam grows by exactly the route's read method,
  not a full Store mirror);
- return clean domain structs (`[]audit.Annotation`, `[]walkthrough.Walkthrough`,
  `[]notes.Note`) the recorder builds with no Postgres;
- have NO verbatim-passthrough GET BFF today, so the consumer half is a
  FIELD-CONTRACT pin on the recorded provider golden (slice 687 D3), not a
  toEqual passthrough drive. Each pins the load-bearing envelope + row-shape
  assumptions a future list-GET BFF (or the existing component consumers) depend
  on, and (paired with the Go recorder) fails on provider drift.

Deferred to spillover **slice 692** (D3): the two control-detail reads that each
carry a distinct, heavier seam shape coupled to Postgres `dbx` rows.

Confidence: **high.**

## D2 — Recorder-helper reuse: each list read rides its package's existing 689 helper unchanged (AC-1)

Each of the three packages already ships the slice-689 shared recorder helper
(`contractrecord_test.go` with `assertContractGolden`, `canonicalizeJSON`, the
lazy `-update` flag, and a routed/driven variant recorder). The three new tests
reuse those helpers verbatim — I only added (a) one method to each stub
(`ListAnnotations` / `List` / `ListForAuthorAndPeriod`), and (b) the new
`TestContract_*List` function + its golden. No new helper, no new file, no new
recorder scaffolding — the seam grows, the test count grows, the infrastructure
is untouched. The two query-param list reads (`walkthroughs` list,
`auditnotes` legacy list) ride the existing variant recorders:

- `walkthroughs` reuses `recordRoutedVariant` with route pattern `/v1/walkthroughs`
  and an equal target (no `{id}` param — chi resolves the bare path fine).
- `auditnotes` reuses `recordThreadVariant` (drives the handler directly; both
  the thread route AND the legacy list read `{audit_period_id}` from a query
  param and require a non-empty `cred.UserID`, which that helper already sets).

Confidence: **high.**

## D3 — Deferred + spilled (slice 692): the two control-detail reads are dbx-row-coupled, each its own seam shape

Deferred (each is its own distinct seam-fixture story, not a one-method
extension of an existing clean-domain seam):

- **`GET /v1/controls/{id}/attest-form`** (`internal/api/controls/attest.go`
  `AttestForm`). The handler reads the control via `h.loadControl` →
  `dbx.GetControlByIDRow` (a `pgtype.UUID`-coupled sqlc row) inside a tenant-GUC
  read tx, then assembles a schema descriptor (`manual_evidence_schema` +
  platform schema kind/version/requires + `caller_can_attest`). There is NO
  injectable read seam today — the read is inlined in the handler — and the
  recorder would have to build a `dbx.GetControlByIDRow` fixture (incl. the
  JSONB schema column). That is a meaningfully different (and heavier) seam
  shape than the clean-domain-struct reads above. (Grill finding: the GET
  surface is `attest-form` ONLY; there is no `GET /v1/controls/{id}/attestations`
  — `attestations` is POST `Submit`. The slice-690 spec's
  "`attestations`/`attest-form`" wording over-named the GET surface; I did not
  invent an `attestations` GET to match it — same discipline as slice 689 D2.)
- **`GET /v1/evidence?control_id=…`** (`internal/api/controldetail` `Evidence`).
  Its own keyset-pagination + `CountEvidenceForTenant` two-method seam over
  `[]dbx.ListEvidenceForControlPagedRow` (Postgres-coupled rows + the unexported
  `evidencePage` cursor struct + `next_cursor` keyset encoding). Left on the
  concrete `*Store` by slices 411/412/689/690; not part of the control-detail
  tab cluster the e2e suite traverses.

Both carried into spillover **slice 692**
(`docs/issues/692-contract-tier-controldetail-attest-form-evidence-window.md`),
status `ready` (the per-route seam pattern is on main).

The slice-687 D3 audit-period passthrough half (add the BFF-drive to slice 687's
field-contract goldens when a single-period BFF lands) remains a no-op until that
BFF exists — it stays tracked in slice 687 D3 / slice 689 D4, not re-spilled here
(nothing to do until the BFF is built).

Confidence: **high.**

## D4 — Field-contract consumer disposition, not toEqual passthrough (AC-2)

All three covered routes get a FIELD-CONTRACT consumer half (slice 687 D3), not
a toEqual BFF-passthrough drive, because none has a verbatim-passthrough GET BFF
today:

- `samples/{id}/annotations` — read by the sample-detail component, not a thin
  passthrough.
- `walkthroughs` list — the list BFF (`web/app/api/audit/walkthroughs/route.ts`)
  is POST-only.
- `audit-notes` legacy list — the workspace reads `/thread`, not the legacy list;
  no GET BFF.

Each consumer test pins the load-bearing assumptions a future list-GET BFF will
depend on (envelope shape `{<rows>:[], count:N}`, count === length, per-row field
types, the omitempty boundaries) and asserts the empty-variant records `[]` (never
null). The audit-notes consumer additionally pins the millisecond
`2006-01-02T15:04:05.000Z` timestamp format `noteWireFrom` emits (distinct from
the RFC3339Nano the other audit-workspace wires use — slice 689 D-revisit note)
and the `parent_note_id`/`depth` omitempty on the flat author-scoped list. When a
verbatim GET BFF lands for any of the three, the toEqual passthrough-drive half
is added (tracked in the revisit section).

Confidence: **high.**

## D5 — Zero-new-gate (AC-4) + coverage

No new CI job, no new gate, no new tool, no new dependency (ADR-0007 (d)). The
three recorders ride the existing `Go · build + test` unit surface; the three
consumers ride `Frontend · vitest` (auto-enrolled by slice 348's `**/*.test.ts`
directory walk). No `ci.yml` change. `go build ./...`, `go vet`, and
`golangci-lint run` on the three changed packages are clean (0 issues); no
`go.mod`/`go.sum` diff (chi / uuid / pgx / audit / walkthrough / notes / authctx
/ credstore / tenancy are all pre-existing).

Coverage: `internal/api/audit`, `internal/api/walkthroughs`, and
`internal/api/auditnotes` are HTTP/RLS handler packages integration-tested
against real Postgres (on the coverage-gate exclude list). The seam refactors
add no uncovered production branch — each grown `reader` field still points at
the same store, so the `ListAnnotations`/`List`/`ListForAuthorAndPeriod` bodies
changed `h.store.X` → `h.reader.X`, identical coverage. No per-package floor to
lift; no ratchet obligation.

## D6 — Drift-sensitivity proof (AC-3)

Proved on the `GET /v1/samples/{id}/annotations` endpoint. Renamed the golden's
`result` key → `verdict` in the populated variant's first row. Result:

- **Provider half failed** (`go test ./internal/api/audit/ -run
TestContract_SampleAnnotationsList`): the `populated` variant reported
  `wire shape drifted from golden` — the handler emits `result`, the golden now
  said `verdict`.
- **Consumer half failed** (`npm run test -- sample-annotations.contract`):
  `populated[0].result: expected 'undefined' to be 'string'` — the
  field-contract row-type assertion.

Both halves red on a single-field rename; golden restored; both green. This is
the slice-210-class catch reproduced on the audit-workspace annotation-list read
— the exact bug class ADR-0007 exists to catch.

---

## Revisit once in use

- **The two deferred control-detail reads.** `attest-form` + the per-control
  Evidence ledger window remain unguarded. Spilled (slice 692).
- **The toEqual upgrade if a list-GET BFF lands.** When a walkthroughs LIST GET
  BFF, an audit-notes LIST GET BFF, or a sample-annotations passthrough BFF
  lands, the corresponding consumer gains a toEqual passthrough drive (currently
  all three are field-contract pins per slice 687 D3).
- **`created_at` millisecond formatting (audit-notes).** The audit-notes-list
  golden records `created_at`/`updated_at` in the `2006-01-02T15:04:05.000Z`
  (millisecond) format `noteWireFrom` emits — distinct from the RFC3339Nano the
  other audit-workspace wires use. If the note wire format is normalized, the
  golden + the consumer's millisecond regex update together.
- **`canonical_hash` encoding (walkthroughs).** The walkthroughs-list golden
  records the hex-encoded digest (64 lowercase hex chars). If a future change
  alters the encoding, the golden + the consumer's `/^[0-9a-f]{64}$/` assert
  update together.

## Confidence

| Decision                                                                   | Confidence |
| -------------------------------------------------------------------------- | ---------- |
| D1 — cover the three audit-workspace LIST reads; defer the two dbx-coupled | high       |
| D2 — reuse each package's existing 689 recorder helper unchanged           | high       |
| D3 — defer attest-form + Evidence window; spill to slice 692               | high       |
| D4 — field-contract consumer disposition (no verbatim GET BFF)             | high       |
| D6 — drift sensitivity proven on the annotations endpoint                  | high       |

## Spillovers

- **slice 692** — contract-tier rollout: controldetail attest-form + per-control
  Evidence ledger window (the two `dbx`-coupled reads slice 690 deferred).
  `ready` — deps (the slice-411/412/687/689/690 per-route seam pattern) are on
  `main`/this PR.
  `docs/issues/692-contract-tier-controldetail-attest-form-evidence-window.md`.
