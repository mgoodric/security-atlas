# Slice 412 — contract-tier tail (controls-detail + audit-workspace) — decisions log

JUDGMENT slice. The build-time subjective calls (which tail routes to cover vs.
defer, the seam shape per targeted route, the golden variants/fixtures, the
passthrough-vs-transform consumer-assert disposition) are recorded here per the
continuous-batch JUDGMENT convention; the maintainer iterates post-deployment.
This does NOT touch the product-runtime AI-assist boundary (separate,
constitutional).

Cross-references: ADR-0007 (`docs/adr/0007-contract-test-tier.md`), slice 411
decisions log (`docs/audit-log/411-contract-tier-controls-audit-decisions.md`,
the per-route Option-A seam + recorder + transform-aware-vs-passthrough-assert
pattern this slice mirrors), slice 412 spec
(`docs/issues/412-contract-tier-controls-audit-tail.md`).

- detection_tier_actual: none
- detection_tier_target: contract

---

## D1 — Route scoping: which tail routes this slice covers, and why (AC-2)

The slice-412 spec lists a large tail across multiple packages
(`ucfcoverage` / `controlstate` / attestations / the controldetail `Evidence`
ledger window / the audit-workspace `audit`+`auditnotes` populations / samples /
walkthroughs / notes reads). The spec's own scope discipline ("prioritize the
highest-e2e-traffic ones, document + spill any further deferrals") plus the
slice-411 D1 precedent (a clean bounded cut + a spillover beats an overreaching
slice) governs the cut.

I inventoried what the `/e2e/` suite actually `route.fulfill`s for the tail
(`grep` over `web/e2e/*.spec.ts`). The control-detail tail the suite still
hand-mocks after slice 411 is exactly three routes, all in
`web/e2e/control-detail-tabs.spec.ts`:

| Route                                 | Package                     | e2e-mocked?               |
| ------------------------------------- | --------------------------- | ------------------------- |
| `GET /v1/controls/{id}/state`         | `internal/api/controlstate` | yes (control-detail-tabs) |
| `GET /v1/controls/{id}/effectiveness` | `internal/api/controlstate` | yes (control-detail-tabs) |
| `GET /v1/controls/{id}/coverage`      | `internal/api/ucfcoverage`  | yes (control-detail-tabs) |

**Targeted (this slice): the two `controlstate` routes.**

| Route                                 | Package      | Why first                                                                                                                                                                       |
| ------------------------------------- | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET /v1/controls/{id}/state`         | controlstate | e2e-mocked; both routes in this package read through ONE clean abstraction (`*eval.Engine`), so a single two-method seam covers both — best traffic-per-seam ratio in the tail. |
| `GET /v1/controls/{id}/effectiveness` | controlstate | e2e-mocked; same seam. Exercises the no-data (`total_count=0`) branch the consumer must not confuse with "perfectly failing".                                                   |

This is a coherent bounded cut: **one new seam, two new endpoint goldens, drift
proof.** It matches the slice-411 shape (one seam per family-package) and drains
the two highest-traffic, cheapest-seam tail routes.

**Deferred (spillover slice 687):** see D5. The remaining tail —
`ucfcoverage`'s `GET /v1/controls/{id}/coverage`, the audit-workspace
populations / samples / walkthroughs / notes reads (`internal/api/audit` +
`internal/api/auditnotes`), the single-period `Get` + `control-state`
(`auditperiods`), the attestation routes, and the controldetail `Evidence`
ledger window — is filed as a follow-on. The `ucfcoverage` deferral is the
load-bearing one (D5 explains the seam-cost asymmetry that drove it).

## D2 — The seam shape: a two-method controlEvaluator, sized to the two routes (AC-1)

`controlstate.Handler` held `engine *eval.Engine`; the `State` and
`Effectiveness` handlers are the only two routes the package serves, and each
reads through exactly one engine method (`ControlState` / `Effectiveness`). So
the seam is a two-method `controlEvaluator` carrying JUST those two methods —
the slice-411 D2 sizing rule ("narrower than the full surface, wider than a
one-method seam because the traffic justifies it"), applied to a package where
the two methods are the whole surface.

**Simplification beyond the slice-411 template:** in `auditperiods` /
`controldetail` the concrete store field is RETAINED on the struct because OTHER
routes in those packages (Create/Get/Freeze/…, the Evidence ledger window) keep
reading it directly. Here BOTH routes read through the seam, so retaining
`engine *eval.Engine` would leave a write-only field (assigned in `New`, never
read) that `golangci-lint`'s `unused` check flags. I removed it: the struct
carries only `evaluator controlEvaluator`, and `New(engine *eval.Engine)` wires
`evaluator: engine`. The public `New(*eval.Engine)` signature is byte-for-byte
unchanged (P0-409-2); only the struct's private shape changed.

**P0 compliance:**

- **P0-409-1 (no recorder on the integration surface):** honored. Both recorders
  run on the plain `go test ./...` unit surface via an injected fixed-row
  `stubEvaluator`. No `//go:build integration` tag on any `*_contract_test.go`.
  Verified: `go test ./internal/api/controlstate/` (no `-tags`) records + asserts
  green.
- **P0-409-2 (no public-API widening):** honored. `controlEvaluator`, the
  `evaluator` field, and `newHandlerWithEvaluator` are all **unexported**. The
  exported `New(*eval.Engine)` signature is unchanged; the `httpserver.go` wiring
  is untouched (`go build ./...` green).
- **No new Go import** outside packages already in `go.mod` (chi / uuid / eval /
  tenancy are all pre-existing). `go build ./...` and `go vet` clean; no
  `go.mod`/`go.sum` diff.
- **AC-4 (zero-new-gate):** no `ci.yml` change. The recorders ride the existing
  `Go · build + test` unit surface; the consumers ride `Frontend · vitest`
  (auto-enrolled by slice 348's `**/*.test.ts` directory walk).

## D3 — The recorder request gates (the chi-routing wrinkle, slice 411 D3)

Both `State` and `Effectiveness` resolve the control id via
`chi.URLParam(r, "id")` — a PATH param. So, like the slice-411 controldetail
recorder (and unlike the auditperiods list recorder, which used a raw
`httptest.NewRequest`), this recorder routes the request through a
`chi.NewRouter()` mounting the handler at the real `/v1/controls/{id}/{state,
effectiveness}` pattern so `chi.URLParam` resolves. A maintainer copying the
auditperiods recorder verbatim would get an empty `{id}` (→ 400) and be puzzled;
recorded explicitly.

Neither handler has a `canWrite`/credential gate (both are reads open to any
authed caller; OPA is the production RBAC gate). The only gate is
`tenantContext`, satisfied by binding a tenant onto the request context with
`tenancy.WithTenant`. No `authctx` credential needed (simpler than the
controldetail recorder, which carries a control-read credential because its
handlers run `requireControlRead` first).

## D4 — Golden shapes: variants exercising the nullable + no-data branches

Each golden is `{_comment, endpoint, variants{}}` with stable
contract-identifier keys (slice 392/409/410/411 D3/D4). Every endpoint records
`populated` + `empty`. The `populated` variants deliberately mix row shapes so a
single capture exercises both the present and absent branches:

- **control-state** — row 1 a scoped cell fully populated (`scope_cell_id`
  present, `last_observed_at` present); row 2 the whole-tenant cell
  (`scope_cell_id` nil → JSON `null`) with no observation yet
  (`last_observed_at` nil → JSON `null`). Pins the nullable
  `scope_cell_id` + `last_observed_at` shape the consumer's `string | null`
  typing depends on. `result` is varied (`pass` / `na`) and `trigger` is varied
  (`evidence` / `schedule`) so the enum-ish fields are pinned as free strings.
- **control-effectiveness** — `populated` carries real data
  (`pass_count` 12 / `total_count` 15 / `pass_rate` 0.8); `empty` carries the
  no-data shape (`total_count` 0 / `pass_rate` 0). This pins the AC-2-class
  contract (slice 256's "no data must NOT degrade to perfectly failing"): the
  consumer test asserts the empty variant is `total_count === 0`, not a missing
  or null field.

Fixture values are obviously-fake (synthetic UUIDs
`…000000000412` / `1111…` / `3333…`, plain enum strings, round numbers) per
slice 314 / GitGuardian — no JWT- or vendor-shaped literals.

## D5 — Why `ucfcoverage`'s `/coverage` is deferred (the seam-cost asymmetry)

`GET /v1/controls/{id}/coverage` is e2e-mocked and is the third control-detail
tail route — so on traffic it belongs in this slice. I deferred it on a
**seam-cost** judgment that mirrors slice 411 D1's "would triple the seam
surface" rationale.

`controlstate`'s two handlers each read through ONE method on a clean `*eval.Engine`
abstraction → a 2-method seam captures the whole package. `ucfcoverage.ControlCoverage`
is structurally different: it is a transaction-orchestrating multi-query
ASSEMBLER. A single request:

1. opens a tenant tx (`h.inTenantTx`) and runs `GetControlByID`,
2. branches on whether the control is anchored (200 + null anchor when not),
3. reads `GetSCFAnchorByID` (catalog),
4. branches on `?framework_version=` (pinned vs unpinned), running one of two
   different `dbx` list queries,
5. optionally runs the slice-256 per-row coverage computation across THREE more
   injected stores (`engine` + `scopeStore` + `fwScopeStore`), each with its own
   sub-queries.

An honest Option-A seam over that path is not a 2-method interface — it is a
6+-method seam over `dbx.Queries` (GetControlByID / GetSCFAnchorByID /
ListRequirementsForAnchor / ListRequirementsForAnchorByFrameworkVersion /
GetFrameworkVersionBySlugAndVersion / …) PLUS a way to fake `inTenantTx` PLUS the
three coverage stores — to record one golden. That is a disproportionate seam
surface for one endpoint, and it risks recording the seam's assembled output
rather than the handler's real serialization branches (the anchored/unanchored
and pinned/unpinned forks are the interesting wire-shape variants, and they live
in the handler, not in any single store method).

Deferring it keeps slice 412 a coherent bounded cut and lets the follow-on slice
design the `ucfcoverage` seam deliberately (likely a narrower read-model method
that returns the assembled `(control, anchor, requirements)` triple the handler
serializes — a design call worth its own slice, not a bolt-on here). Filed as
slice 687, `ready` (the seam pattern this slice extends is on `main` with this
PR).

## D6 — The consumer asserts: both are verbatim passthroughs (slice 411 D5 confirmed)

Slice 411 D5's finding was that the control-detail tab BFFs are verbatim
passthroughs (unlike slice 410's transforming risks BFF). I checked both
targeted BFFs and confirmed the same:

| Route                                 | BFF                                                | Disposition                                                                                                                  |
| ------------------------------------- | -------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| `GET /v1/controls/{id}/state`         | `web/app/api/controls/[id]/state/route.ts`         | **Passthrough.** `getControlState` returns `res.json()` unchanged; route does `NextResponse.json(state)`. `toEqual(golden)`. |
| `GET /v1/controls/{id}/effectiveness` | `web/app/api/controls/[id]/effectiveness/route.ts` | **Passthrough.** `getControlEffectiveness` returns `res.json()` unchanged. `toEqual(golden)`.                                |

So both consumer asserts use `toEqual(golden)` plus the load-bearing field
contract (states-never-null, nullable-fields-present-when-null, all-effectiveness-
numbers-present) and the 401-before-upstream guard. I was transform-AWARE
(checked each BFF) and the answer was uniformly passthrough; recorded so the
conclusion is auditable rather than assumed.

## D7 — Drift-sensitivity proof (AC-3)

Proved on the **control-state** endpoint. Renamed the golden's `freshness_status`
key → `freshness` (both rows of the `populated` variant). Result:

- **Provider half failed** (`go test ./internal/api/controlstate/ -run
TestContract_ControlState`): `variant "populated" wire shape drifted from
golden` — the handler emits `freshness_status`, the golden now says
  `freshness`.
- **Consumer half failed** (`npm run test -- control-state.contract.test.ts`):
  `populated.freshness_status: expected 'undefined' to be 'string'` (the field
  contract assertion).

Both halves red on a single-field rename; golden restored; both green. This is
the slice-210-class catch (the BE/FE contract gap that hid the login form),
reproduced on a control-detail tail route — the exact bug class ADR-0007 exists
to catch.

## D8 — Zero-new-gate (AC-4) + coverage

No new CI job, no new gate, no new tool, no new dependency (ADR-0007 (d)). The
recorders ride the existing `Go · build + test` unit surface; the consumers ride
`Frontend · vitest`.

Coverage: `internal/api/controlstate/` is on the coverage-gate **exclude** list
(slice 069 / 312: HTTP/RLS handler package integration-tested against real
Postgres). It previously had NO in-package unit test file (0% unit surface). The
recorder gives it a first unit-surface suite. No per-package floor to lift; no
ratchet obligation. The seam change adds no uncovered production branch (the
`evaluator` field + `New(…, evaluator: engine)` is the same single statement,
integration-covered; the two handler bodies changed `h.engine.X` → `h.evaluator.X`,
identical coverage).

---

## Revisit once in use

- **slice 687 (the deferred tail).** The `ucfcoverage` `/coverage` seam is the
  load-bearing follow-on — design it deliberately (D5). The audit-workspace
  reads + attestations + controldetail Evidence window round out the tail.
- **The `evaluated_at` RFC3339-vs-Nano shape.** The fixture timestamps have no
  sub-second component, so the golden records `evaluated_at` as
  `2026-04-02T09:00:00Z` (no fractional digits). The handler uses
  `RFC3339Nano`, which emits fractional digits only when present. If a future
  consumer parses `evaluated_at` with a format that REQUIRES fractional digits,
  the contract would need a sub-second fixture to pin that. Low risk (ISO-8601
  parsers handle both), but noted.
- **The control-effectiveness "no data" contract.** The `empty` variant pins
  `total_count=0`/`pass_rate=0`. If a future product decision changes the
  no-data shape (e.g. emitting `pass_rate: null` to make "no data" explicit on
  the wire), the golden + the consumer's `empty.total_count === 0` assert must
  both update in the same PR.

## Confidence

| Decision                                                 | Confidence |
| -------------------------------------------------------- | ---------- |
| D1 — cover the two `controlstate` routes, defer the rest | high       |
| D2 — two-method seam; drop the now-dead `engine` field   | high       |
| D5 — defer `ucfcoverage` on seam-cost asymmetry          | high       |
| D6 — both BFFs are verbatim passthroughs                 | high       |
| D7 — drift sensitivity proven                            | high       |
| D4 — golden variant shapes exercise the right branches   | medium     |

## Spillovers

- **slice 687** — contract-tier rollout: controls-detail + audit-workspace
  REMAINING tail (`ucfcoverage` `/coverage`; the audit-workspace
  populations/samples/walkthroughs/notes reads in `internal/api/audit` +
  `internal/api/auditnotes`; the single-period `Get` + `control-state` in
  `auditperiods`; the attestation routes; the controldetail `Evidence` ledger
  window). `ready` — deps (the slice-411/412 per-route seam pattern) merge with
  this PR. `docs/issues/687-contract-tier-tail-remaining.md`.
