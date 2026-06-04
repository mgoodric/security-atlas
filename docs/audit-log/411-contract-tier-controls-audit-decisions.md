# Slice 411 — contract-tier goldens for controls-detail + audit-workspace routes — decisions log

JUDGMENT slice. The build-time subjective calls (the per-route-family seam
shapes, the targeted-vs-deferred route scoping, the golden variants/fixtures,
the passthrough-vs-transform consumer-assert dispositions) are recorded here per
the continuous-batch JUDGMENT convention; the maintainer iterates
post-deployment. This does NOT touch the product-runtime AI-assist boundary
(separate, constitutional).

Cross-references: ADR-0007 (`docs/adr/0007-contract-test-tier.md`), slice 409
decisions log D1 + D6 (`docs/audit-log/409-contract-tier-rollout-dashboard-decisions.md`),
slice 410 decisions log D1 + D4 (`docs/audit-log/410-contract-tier-risks-panel-decisions.md`,
the list-only-seam + transform-aware-assert precedent), slice 411 spec
(`docs/issues/411-contract-tier-controls-audit-routes.md`).

---

## D1 — Route scoping: which routes this slice covers, and why

The slice 411 spec is explicit that the controls-detail (`/v1/controls/*`) and
audit-workspace (`/v1/audit/*`) families are large, "likely split into two
slices," and that the bar is "the highest-traffic controls-detail routes AND the
highest-traffic audit-workspace routes the e2e suite traverses (aim for the
394-hand-mocked set)." A clean bounded slice + a spillover beats an overreaching
one.

I inventoried what the `/e2e/` suite actually `route.fulfill`s for these
families (`grep` over `web/e2e/*.spec.ts`):

- `web/e2e/control-detail-tabs.spec.ts` mocks `/api/controls/{id}/coverage`,
  `/state`, `/policies`, `/risks`, `/history`.
- `web/e2e/audits-header.spec.ts` asserts the in-progress pill consumes the
  existing `/api/audits` BFF (→ upstream `GET /v1/audit-periods`).

**Targeted (this slice):**

| Route                            | Package                      | Why first                                                                                                                                                                          |
| -------------------------------- | ---------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET /v1/controls/{id}/policies` | `internal/api/controldetail` | e2e-mocked; the controldetail `Handler` serves three of the five control-detail-tab reads over ONE `*Store` — a single narrow seam covers all three (best traffic-per-seam ratio). |
| `GET /v1/controls/{id}/risks`    | `internal/api/controldetail` | e2e-mocked; same seam. Exercises the opaque score-blob + nullable `link_weight` branches.                                                                                          |
| `GET /v1/controls/{id}/history`  | `internal/api/controldetail` | e2e-mocked; same seam. Exercises the nullable `scope_cell` + `next_cursor` branches.                                                                                               |
| `GET /v1/audit-periods`          | `internal/api/auditperiods`  | e2e-traversed via `/api/audits`; the audit-workspace family's index route. One list-only seam (slice 410 precedent). Exercises the `frozen_*` omitempty branch.                    |

This is a coherent first cut: **one seam per family-package**, covering the
control-detail tab cluster the v1-binary control-detail view traverses PLUS the
audit-workspace period index. Four endpoint pairs, two new seams.

**Deferred (spillover slice 412):** the controls-detail tail
(`coverage`/`effectiveness`/`state`/`attestations` — these live in SEPARATE
packages `ucfcoverage`/`controlstate`/attest, each needing its own seam, so
folding them in would triple the seam surface) and the audit-workspace tail
(single-period `Get`/`control-state`, `audit-notes`+`thread`,
populations/samples/walkthroughs reads). Filed as `ready` (deps merged). The
controldetail `Evidence` handler (tenant-wide ledger window, NOT a
control-detail tab read) is also left on the concrete `*Store` and spilled —
extending the existing `controlDetailReader` seam to it is a 412 judgment call.

## D2 — The seam shapes: narrow per-route, not god-interfaces (AC-1)

Both families took the slice 410 list-only Option-A pattern, sized to the routes
each slice-411 seam serves:

**controldetail — a three-method `controlDetailReader`.** The controldetail
`Handler` holds `store *Store`; the `*Store` serves four routes (Evidence +
Policies + Risks + History). A full read interface would carry all four reads
PLUS the two evidence-ledger helpers (`EvidencePaged`, `CountEvidenceForTenant`)
— a six-method interface for three goldens. Instead `controlDetailReader`
carries JUST the three methods the targeted routes use
(`PoliciesForControl`/`RisksForControl`/`HistoryForControl`); the `Evidence`
handler keeps reading the concrete `h.store` directly. This is a genuine
three-method seam (not a one-method one) because the three routes are co-located
on one package over one store and naturally share a seam — narrower than the
full surface, wider than slice 410's single-method `riskLister` because the
traffic justifies it.

**auditperiods — a one-method `periodLister`.** The auditperiods `Handler`
holds `store *period.Store` exposing a WIDE surface
(Create/Get/List/Freeze/ControlState/AttachPopulation/…). Exactly the slice 410
case: a list-only `periodLister` carrying JUST `List(ctx) ([]period.Period,
error)` — the one method the targeted `GET /v1/audit-periods` route uses. Every
other handler keeps the concrete `h.store`.

**P0 compliance (both):**

- **P0-409-1 / P0-411 (no recorder on the integration surface):** honored. All
  four recorders run on the plain `go test ./...` unit surface via injected
  fixed-row stubs. No `//go:build integration` tag on any `*_contract_test.go`.
  Verified: `go test ./internal/api/controldetail/ ./internal/api/auditperiods/`
  (no `-tags`) records + asserts green.
- **P0-409-2 (no public-API widening):** honored. `controlDetailReader`,
  `periodLister`, the `reader`/`lister` fields, and `newHandlerWithReader`/
  `newHandlerWithLister` are all **unexported**. The exported `New(*Store)` /
  `New(*period.Store)` signatures are byte-for-byte unchanged; each wires the
  seam field to the same store, so production behavior is identical and the
  `httpserver.go` wiring is untouched. Only the static type the targeted paths
  read through changed (concrete → interface the concrete already satisfies).
- **No new Go import** outside packages already in `go.mod` (chi / uuid /
  pgtype / authctx / credstore / dbx / period / tenancy are all pre-existing).
  `go mod tidy` produced no diff; `go.mod`/`go.sum`/`go.work.sum` unchanged.

## D3 — The recorder request gates (the controldetail chi-routing difference)

Both families' targeted handlers run an auth gate BEFORE tenant resolution
(slice 410 D2 flagged this class for the risks endpoint):

- **controldetail** `Policies`/`Risks`/`History` run `requireControlRead` FIRST
  (defense-in-depth role guard, authz.go) then `tenantContext`. So the recorder
  request carries BOTH an `authctx` credential with a control-read signal
  (`IsApprover: true` → `grc_engineer` in `hasControlRead`) AND a tenant
  (`tenancy.WithTenant`). **The new wrinkle vs slice 410:** the control id is a
  PATH param resolved via `chi.URLParam(r, "id")`, not a query param. So unlike
  the risks recorder (which used a raw `httptest.NewRequest`), this recorder
  routes the request through a `chi.NewRouter()` mounting the handler at
  `/v1/controls/{id}/{policies,risks,history}` so `chi.URLParam` resolves.
  Recorded explicitly because a maintainer copying the risks recorder verbatim
  would get an empty `{id}` (→ 400) and be puzzled.
- **auditperiods** `List` runs `authnContext` (credential with non-empty
  `TenantID` + tenant on context) but has NO `canWrite` gate (List is a read
  open to any authed caller; OPA is the production RBAC gate). So its recorder
  carries a bare credential (`TenantID` set, no role flags) + a tenant. No chi
  routing needed (no path param).

## D4 — Golden shapes: variants (populated + empty) exercising nullable branches

Each golden is `{_comment, endpoint, variants{}}` with stable
contract-identifier keys (slice 392/409/410 D3). Every endpoint records
`populated` + `empty`. The `populated` variants deliberately mix row shapes so a
single capture exercises both the present and absent branches of the nullable
fields:

- **control-policies** — two rows with distinct `version`/`status` values.
- **control-risks** — row 1 fully populated (opaque `inherent_score`/
  `residual_score` JSON blobs, present `link_weight` 0.75); row 2 minimal (nil
  score blobs → handler's `jsonOrNull` emits JSON `null`; NULL `design_score` →
  `numericToFloat` emits `link_weight: null`). Pins the opaque-blob +
  number-or-null contract the BFF's `ControlLinkedRisk` typing depends on.
- **control-history** — row 1 `scope_cell` present; row 2 `scope_cell` NULL
  (`uuidPtr` → `null`). Pins the nullable `scope_cell` + the `next_cursor: ""`
  (no-next-page) wire shape.
- **audit-periods** — row 1 open period (`frozen_at`/`frozen_hash`/`frozen_by`
  ABSENT on the wire — the `omitempty` branch); row 2 frozen period (all three
  present, `frozen_hash` the hex-encoded digest). Pins the absent-not-null shape
  the consumer's optional `frozen_*` typing depends on.

Fixture values are obviously-fake (synthetic UUIDs `…000000000411` /
`11111111…` / `22222222…` etc., a 32-byte non-hash digest of repeating
`aabbccdd…` bytes, plain strings) per slice 314 / GitGuardian — no JWT- or
vendor-shaped literals.

## D5 — The consumer asserts: ALL FOUR are verbatim passthroughs (the slice-410 difference inverted)

Slice 410's load-bearing finding was that the dashboard/risks BFF TRANSFORMS
(unwraps `body.risks`, re-wraps `{risks, count}`), so its consumer assert had to
be transform-aware. I checked each of the four targeted BFFs and found the
**opposite**: all four are verbatim passthroughs.

| Route                            | BFF                                           | Disposition                                                                                                                                                                  |
| -------------------------------- | --------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET /v1/controls/{id}/policies` | `web/app/api/controls/[id]/policies/route.ts` | **Passthrough.** `getControlPolicies` returns `res.json()` unchanged; route does `NextResponse.json(body)`. `toEqual(golden)`.                                               |
| `GET /v1/controls/{id}/risks`    | `web/app/api/controls/[id]/risks/route.ts`    | **Passthrough.** `getControlRisks` returns `res.json()` unchanged. `toEqual(golden)`. (Distinct from slice 410's dashboard/risks BFF — different route, different consumer.) |
| `GET /v1/controls/{id}/history`  | `web/app/api/controls/[id]/history/route.ts`  | **Passthrough.** `getControlHistory` returns `res.json()` unchanged. `toEqual(golden)`.                                                                                      |
| `GET /v1/audit-periods`          | `web/app/api/audits/route.ts`                 | **Passthrough.** Re-emits the upstream body text verbatim (`new NextResponse(body, …)`). `toEqual(golden)` (both sides parse JSON, so canonicalization is moot).             |

So all four consumer asserts use `toEqual(golden)` like the slice-409 dashboard
panels — NOT slice 410's `{risks, count: risks.length}` re-wrap. I was
transform-AWARE (checked each BFF) and the answer was uniformly passthrough;
recording the disposition explicitly so the conclusion is auditable rather than
assumed. Each consumer test also asserts the load-bearing field contract
(arrays-never-null, opaque-blobs-present, nullable-fields-typed-when-present) and
the 401-before-upstream guard.

## D6 — Drift-sensitivity proof (AC-3)

Proved on the **control-risks** endpoint. Renamed the golden's `link_weight`
key → `weight` (both rows of the `populated` variant). Result:

- **Provider half failed** (`go test ./internal/api/controldetail/ -run
TestContract_ControlRisks`): `variant "populated" wire shape drifted from
golden` — the handler emits `link_weight`, the golden now says `weight`.
- **Consumer half failed** (`npm run test -- lib/contracts/control-risks.contract.test.ts`):
  `populated.link_weight: expected false to be true` (the
  `"link_weight" in rk` field-presence assertion).

Both halves red on a single-field rename; golden restored; both green. This is
the slice-210-class catch (the BE/FE contract gap that hid the login form),
reproduced on a control-detail route — the exact bug class ADR-0007 exists to
catch.

## D7 — Zero-new-gate (AC-4) + coverage

No new CI job, no new gate, no new tool, no new dependency (ADR-0007 (d)). The
recorders ride the existing `Go · build + test` unit surface; the consumers ride
`Frontend · vitest` (auto-enrolled by slice 348's `**/*.test.ts` directory
walk).

Coverage:

- **`internal/api/auditperiods/`** is on the coverage-gate **exclude** list
  (slice 069 / 312: HTTP/RLS handler package integration-tested against real
  Postgres). It previously had NO in-package unit test file at all (0% unit
  surface). The recorder gives it a first unit-surface suite: **0% → 14.0%**. No
  per-package floor to lift; no ratchet obligation.
- **`internal/api/controldetail/`** carries a hard **90% floor** (slice 069),
  measured on the MERGED unit+integration profile (slice 279). My change adds no
  uncovered production branch: the `controlDetailReader` interface declaration
  has no statements; the `reader` field + `New(…, reader: store)` is the same
  single statement (already integration-covered); `newHandlerWithReader` is
  100% covered by the new unit recorder; the three handler bodies changed
  `h.store.X` → `h.reader.X` (identical coverage, the integration suite still
  drives them through `New`). The recorder ADDS unit-surface coverage on top.
  Merged coverage is monotonic-non-decreasing; the 90% floor holds. No floor
  change in this PR (the ratchet is untouched).

## Detection-tier classification (slice 353 Q-13)

- `detection_tier_actual`: `none` — no product bug surfaced during the slice;
  the drift-sensitivity rename (D6) was a deliberate proof, not a found defect.
- `detection_tier_target`: `contract` — this slice's entire purpose is to add
  the contract tier that catches the slice-210 class of BFF↔atlas shape drift on
  the control-detail + audit-workspace routes at the cheapest surface.

## Spillovers

- **slice 412** — contract-tier rollout: controls-detail + audit-workspace LONG
  TAIL (`coverage`/`effectiveness`/`state`/`attestations` in the separate
  `ucfcoverage`/`controlstate`/attest packages; the audit-workspace
  populations/samples/walkthroughs/notes reads; the controldetail `Evidence`
  ledger window). `ready` — deps (slice 411 seam pattern) merge with this PR.
  `docs/issues/412-contract-tier-controls-audit-tail.md`.
