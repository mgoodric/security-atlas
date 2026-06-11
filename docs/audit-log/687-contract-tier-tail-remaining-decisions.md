# Slice 687 — contract-tier tail remaining (ucfcoverage + audit-workspace reads) — decisions log

JUDGMENT slice. The build-time subjective calls (which remaining tail routes to
cover vs. defer, the seam shape per targeted route — most load-bearingly the
`ucfcoverage` `/coverage` seam slice 412 D5 deferred — the golden variants, and
the passthrough-vs-field-contract consumer-assert disposition) are recorded here
per the continuous-batch JUDGMENT convention; the maintainer iterates
post-deployment. This does NOT touch the product-runtime AI-assist boundary
(separate, constitutional).

Cross-references: ADR-0007 (`docs/adr/0007-contract-test-tier.md`), slice 412
decisions log D5 (`docs/audit-log/412-contract-tier-tail-decisions.md`, the
`ucfcoverage` deferral rationale this slice resolves), slice 411 decisions log
(`docs/audit-log/411-contract-tier-controls-audit-decisions.md`, the per-route
Option-A seam + recorder + transform-aware-vs-passthrough-assert pattern this
slice mirrors), slice 687 spec
(`docs/issues/687-contract-tier-tail-remaining.md`).

- detection_tier_actual: contract
- detection_tier_target: contract

---

## D1 — `ucfcoverage` `/coverage`: land it via a thin read-model seam (resolves slice 412 D5)

Slice 412 D5 deferred `GET /v1/controls/{id}/coverage` on a seam-cost judgment:
an HONEST Option-A seam over `ucfcoverage.ControlCoverage` — a
transaction-orchestrating multi-query assembler — looked like a 6+-method
`dbx.Queries` mirror PLUS an `inTenantTx` fake PLUS the three slice-256 coverage
stores, to record one golden. D5 itself named the cheaper path: "a recorder over
the assembled response may be cheaper than a full seam … likely a narrower
read-model method that returns the assembled `(control, anchor, requirements)`
triple the handler serializes." This slice takes exactly that path.

**The seam: a single-method `coverageAssembler`.** I split the read from the
serialization. `assembleCoverage(ctx, controlID, fvParam) (coverageView, bool,
error)` (control_coverage.go) does ALL the DB work — the tenant-tx control
lookup, the catalog anchor read, the pinned/unpinned requirements list, AND the
slice-256 per-row coverage computation — and returns an assembled `coverageView`.
`ControlCoverage` then turns that view into the `{ control, anchor,
requirements[] }` wire shape. The recorder injects a fixed-`coverageView` stub
via the unexported `newHandlerWithAssembler`, so the wire shape records on the
plain `go test ./...` unit surface with NO pool, NO eval/scope/framework_scope
stores (ADR-0007 / P0-409-1).

**Why this is faithful, not a cop-out.** D5's stated worry was that a seam might
record "the seam's assembled output rather than the handler's real serialization
branches (the anchored/unanchored and pinned/unpinned forks … live in the
handler, not in any single store method)." That worry is satisfied here BECAUSE
the three interesting forks are SERIALIZATION decisions and they STAYED in
`ControlCoverage`:

| Fork               | Lives in          | Recorded variant    |
| ------------------ | ----------------- | ------------------- |
| anchored, unpinned | `ControlCoverage` | `anchored_unpinned` |
| anchored, pinned   | `ControlCoverage` | `anchored_pinned`   |
| unanchored         | `ControlCoverage` | `unanchored`        |

The seam returns `Anchored bool` + the assembled rows; the handler decides
`anchor: null` + `requirements: []` for the unanchored case, and emits the
anchor + rows otherwise. The recorder drives all three through the REAL handler,
so the goldens pin the real serialization. The `anchored_unpinned` variant
deliberately carries BOTH an in-scope row (`coverage: 0.64`) and an
out-of-scope/no-data row (`coverage: null`), pinning the slice-256 P0-1 contract
(null must NOT degrade to 0) the consumer's `number | null` typing depends on.

**P0 compliance:** `coverageAssembler`, the `assembler` field, and
`newHandlerWithAssembler` are all unexported. `New(*pgxpool.Pool)` is unchanged
and points `assembler` at the Handler itself (`h.assembler = h`), so production
behavior is byte-identical (`assembleCoverage` reads the same `h.engine` /
`h.scopeStore` / `h.fwScopeStore` the old inline body did — and `AttachCoverage`
still mutates those fields after `New`, which the closure-free method picks up).
The `httpserver.go` wiring is untouched.

Confidence: **high.**

## D2 — `auditperiods` Get + ControlState: extend the slice-411 seam to a two-method `periodReader`

Slice 411 carved a list-only `periodLister` seam for `GET /v1/audit-periods`.
The two remaining audit-period READ routes the slice file lists —
`GET /v1/audit-periods/{id}` (Get) and `GET /v1/audit-periods/{id}/control-state`
— read through the wider `*period.Store`. I added a two-method `periodReader`
seam sized to exactly those two methods (slice 412 D2 sizing rule), wired by
`New(*period.Store)` to the same store. The write handlers
(Create/Freeze/AttachPopulation) keep using the concrete `h.store`.

The `ControlState` recorder routes through a chi router (the `{id}` path param)
AND binds an `IsAdmin` credential (the handler's `canWrite` gate; the `Get`
handler has no such gate). Goldens: `audit-period-get` (open + frozen variants —
the frozen variant pins `frozen_at`/`frozen_hash` hex/`frozen_by`, the open
variant pins their omitempty absence AND the absence of `framework_label`, which
is List-only per slice 680) and `audit-period-control-state` (populated +
empty — empty pins `observations: []` + `count: 0`, never null).

Confidence: **high** (seam shape) / see D3 (consumer-half caveat).

## D3 — The audit-period consumer halves are FIELD-CONTRACT pins, not BFF drives (honest scoping)

A genuine ADR-0007 contract has two halves: the provider records, the consumer
(the BFF) asserts its picture against that record. While wiring the audit-period
consumer tests I checked the BFF surface and found a gap the slice file did not
anticipate:

**There is NO current Next.js BFF that consumes `GET /v1/audit-periods/{id}` or
`/control-state`.** The audit-workspace reads the caller-scoped
`GET /v1/me/audit-period` (singular — the auditor's assigned period;
`web/lib/api/audit.ts` `getAuditPeriod`) and `GET /v1/me/audit-periods`. The
`/v1/audit-periods/{id}` + `/control-state` atlas routes exist and are
integration-tested, but no consumer fetches them today (verified:
`grep -rn "audit-periods/{" web/lib web/app` → only the goldens themselves).

So for these two routes the consumer half is a **field-contract assert on the
recorded provider golden**, NOT a BFF-passthrough drive. It pins the load-bearing
wire-shape assumptions a future single-period / sampling BFF will depend on, and
— PAIRED WITH THE GO PROVIDER RECORDER — it still fails on provider drift (the
Go recorder catches the rename; the field-contract vitest catches the consumer
assumption break). This is the honest shape: a provider-pin + field-contract is
weaker than a full passthrough contract but is real regression value, and it is
the correct shape WHEN NO CONSUMER EXISTS YET. When a single-period BFF lands,
the passthrough-drive half is added (spilled — see Spillovers).

The `ucfcoverage` `/coverage` consumer half, by contrast, IS a full
BFF-passthrough drive: `web/app/api/controls/[id]/coverage/route.ts` is a real
verbatim passthrough (`getControlCoverage` returns `res.json()` unchanged;
route does `NextResponse.json(coverage)`), e2e-mocked in
`web/e2e/control-detail-tabs.spec.ts`. That is the load-bearing one and it gets
the full treatment (provider record + BFF drive + drift proof).

Confidence: **high** (the gap is verified, not assumed).

## D4 — Route scoping: what this slice covers, and what it defers (AC-2)

The slice-687 tail is large (the slice file lists `ucfcoverage` /coverage, the
audit-workspace populations/samples/walkthroughs/notes reads, the auditperiods
single Get + control-state, the attestation routes, and the controldetail
Evidence ledger window). Per the slice-411 D1 / slice-412 D1 precedent (a clean
bounded cut + a spillover beats an overreaching slice) and the slice file's own
"cover the highest-e2e-traffic routes first … document + spill the remainder":

**Covered (this slice):**

| Route                                      | Package        | Consumer half        | e2e-mocked                |
| ------------------------------------------ | -------------- | -------------------- | ------------------------- |
| `GET /v1/controls/{id}/coverage`           | `ucfcoverage`  | full BFF passthrough | yes (control-detail-tabs) |
| `GET /v1/audit-periods/{id}`               | `auditperiods` | field-contract (D3)  | no (no consumer yet)      |
| `GET /v1/audit-periods/{id}/control-state` | `auditperiods` | field-contract (D3)  | no (no consumer yet)      |

The `/coverage` route is the load-bearing one (slice 412 D5's deferred target)
and is fully covered. The two audit-period routes are the slice file's explicit
audit-workspace tail and ship as provider-pins + field-contracts (D3).

**Deferred (spillover — see Spillovers):** the audit-workspace
populations/samples/walkthroughs/notes reads (`internal/api/audit` +
`internal/api/auditnotes` + `internal/api/walkthroughs`), the attestation routes
(`internal/api/controls/attest.go`), and the controldetail `Evidence` ledger
window. These each carry their own store seam + auth-gate cost; folding them in
would overreach a coherent cut. The audit-workspace populations/samples BFFs are
also POST/create surfaces in places (`web/app/api/audit/populations/route.ts` is
a POST), so the read-contract subset there needs its own inventory.

Confidence: **high.**

## D5 — Zero-new-gate (AC-4) + coverage

No new CI job, no new gate, no new tool, no new dependency (ADR-0007 (d)). The
two recorders ride the existing `Go · build + test` unit surface; the three
consumers ride `Frontend · vitest` (auto-enrolled by slice 348's `**/*.test.ts`
directory walk). No `ci.yml` change. `go build ./...`, `go vet`, and
`golangci-lint run` on both changed packages are clean (0 issues); no
`go.mod`/`go.sum` diff (chi / uuid / pgx / period / tenancy / authctx are all
pre-existing).

Coverage: `internal/api/ucfcoverage/` and `internal/api/auditperiods/` are both
on the coverage-gate exclude list (HTTP/RLS handler packages integration-tested
against real Postgres). The seam refactors add no uncovered production branch —
`assembleCoverage` is the same statements the old inline `ControlCoverage` body
ran, integration-covered; the auditperiods `reader` field points at the same
store, so the Get/ControlState bodies changed `h.store.X` → `h.reader.X`,
identical coverage. No per-package floor to lift; no ratchet obligation.

## D6 — Drift-sensitivity proof (AC-3)

Proved on the `/coverage` endpoint. Renamed the golden's `coverage` key →
`coverage_score` across all rows. Result:

- **Provider half failed** (`go test ./internal/api/ucfcoverage/ -run
TestContract_ControlCoverage`): `variant "anchored_unpinned" wire shape
drifted from golden` — the handler emits `coverage`, the golden now says
  `coverage_score`.
- **Consumer half failed** (`npm run test -- control-coverage.contract.test.ts`):
  `anchored_pinned.coverage present: expected false to be true` (the field
  contract assertion).

Both halves red on a single-field rename; golden restored; both green. This is
the slice-210-class catch reproduced on the load-bearing control-detail coverage
route — the exact bug class ADR-0007 exists to catch, on the exact route slice
412 D5 deferred.

---

## Revisit once in use

- **The deferred audit-workspace read tail.** populations/samples/walkthroughs/
  notes reads + attestations + controldetail Evidence window remain unguarded.
  Spilled (see below).
- **The audit-period passthrough half.** When a single-period or audit-sampling
  BFF lands that consumes `GET /v1/audit-periods/{id}` or `/control-state`, the
  D3 field-contract consumer halves should gain the passthrough-drive half (the
  goldens are already in place; only the BFF-drive test is missing).
- **`frozen_hash` encoding.** The golden records the hex-encoded SHA-256 (64
  lowercase hex chars). If a future export changes the encoding (e.g. base64),
  the golden + the consumer's `/^[0-9a-f]{64}$/` assert update together.

## Confidence

| Decision                                                       | Confidence |
| -------------------------------------------------------------- | ---------- |
| D1 — land `ucfcoverage` /coverage via a thin read-model seam   | high       |
| D2 — two-method `periodReader` seam for Get + ControlState     | high       |
| D3 — audit-period consumer halves are field-contracts (no BFF) | high       |
| D4 — cover 3 routes, defer the audit-workspace read tail       | high       |
| D6 — drift sensitivity proven on /coverage                     | high       |

## Spillovers

- **slice 689** — contract-tier rollout: audit-workspace read tail
  (populations / samples / walkthroughs / notes reads in `internal/api/audit` +
  `internal/api/auditnotes` + `internal/api/walkthroughs`; the attestation
  routes in `internal/api/controls/attest.go`; the controldetail `Evidence`
  ledger window). `ready` — deps (the slice-411/412/687 per-route seam pattern)
  are on `main` with this PR. `docs/issues/689-contract-tier-audit-workspace-read-tail.md`.
