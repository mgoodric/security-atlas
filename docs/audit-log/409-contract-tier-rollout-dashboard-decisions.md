# Slice 409 — contract-tier rollout: dashboard routes — decisions log

JUDGMENT slice. The build-time subjective calls (the per-endpoint DB-seam
strategy, golden shapes, the 394 reassessment) are recorded here per the
continuous-batch JUDGMENT convention; the maintainer iterates
post-deployment. This does NOT touch the product-runtime AI-assist
boundary (separate, constitutional).

Cross-references: ADR-0007 (`docs/adr/0007-contract-test-tier.md`), slice
392 (`docs/issues/392-contract-test-tier-rollout.md` +
`docs/audit-log/392-contract-test-tier-rollout-decisions.md`), slice 394
(`docs/issues/394-e2e-mocks-load-from-contract-goldens.md`), slice 409
spec (`docs/issues/409-contract-tier-dashboard-routes.md`).

---

## D1 — The per-endpoint DB-seam strategy (AC-1)

ADR-0007's load-bearing constraint: the provider recorder MUST run on the
plain `go test ./...` unit surface (no DB). The dashboard panel handlers
read tenant data through a `*Store` that holds a pgx pool — exactly the
situation slice 392 hit with `GET /v1/metrics` and deferred. The slice
offered three strategies (A: injectable seam; B: no-DB degenerate path; C:
defer). I evaluated each target endpoint and chose:

| Endpoint                                  | Package                           | Strategy                     | Rationale                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| ----------------------------------------- | --------------------------------- | ---------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET /v1/frameworks/posture`              | `internal/api/dashboard`          | **A — covered**              | The wire-shape transformation (row → `postureWire` → JSON envelope) lives entirely in the handler; the DB access is one `*Store` method returning typed `dbx` rows. Introduced an unexported `reader` seam covering the three dashboard read methods; `*Store` satisfies it; the recorder injects a fixed-row stub. No Postgres. Bounded, ADR-0007-faithful (Option A as 392 named for the metrics fix).                                                                                                                                                                                                                                |
| `GET /v1/activity`                        | `internal/api/dashboard`          | **A — covered**              | Same `reader` seam. The handler's keyset pagination + `jsonOrNull` summary handling are real code paths the stub exercises; the null-summary variant pins that `summary` is forwarded as JSON `null`, never absent.                                                                                                                                                                                                                                                                                                                                                                                                                     |
| `GET /v1/upcoming`                        | `internal/api/dashboard`          | **A — covered**              | Same `reader` seam. The `anyToString` title coercion + envelope shape record on the unit surface.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| `GET /v1/evidence/freshness`              | `internal/api/freshnessdrift`     | **A — covered**              | Handler depends on `*freshness.Store` (one `List` method). Introduced an unexported `freshnessLister` seam; the recorder injects a fixed-row stub. The handler's class-bucketing + stale-count rollup + `"unclassified"` defaulting are real transformations the stub exercises.                                                                                                                                                                                                                                                                                                                                                        |
| `GET /v1/controls/drift`                  | `internal/api/freshnessdrift`     | **A — covered**              | Handler depends on `*drift.Store` (one `Report` method). Introduced an unexported `driftReporter` seam; the recorder injects a fixed-row report. The handler's date-formatting + flipped-out row mapping record on the unit surface.                                                                                                                                                                                                                                                                                                                                                                                                    |
| `GET /v1/risks` (dashboard/risks panel)   | `internal/api/risks`              | **C — DEFERRED (spillover)** | The risks `Handler` holds `store *risk.Store` exposing a WIDE surface (Create / List / Get / Delete / Heatmap / ThemeOrgUnitHeatmap …). An Option-A read seam for one endpoint would be a ~7-method interface — a far bigger refactor than recording one golden justifies (the slice's "bigger seam than recording justifies → spillover" case). ALSO: the dashboard/risks BFF is NOT verbatim passthrough — it unwraps `body.risks` and re-wraps `{risks, count}`, so the golden would pin the upstream `/v1/risks` envelope, not the BFF output, needing a transform-aware consumer assert. Deferred with rationale; spillover filed. |
| Controls detail routes (`/v1/controls/*`) | `internal/api/controldetail` etc. | **C — DEFERRED (spillover)** | Large multi-route surface, pool-backed, outside the dashboard-panel core. Same "bigger seam than recording justifies" reasoning. Spillover filed.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| Audit-workspace routes (`/v1/audit/*`)    | `internal/api/audit*`             | **C — DEFERRED (spillover)** | Large multi-route surface, pool-backed, outside the dashboard-panel core. Spillover filed.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |

**P0 compliance:**

- **P0-409-1 (no recorder on the integration surface):** honored. All five
  recorders run on the plain `go test ./...` unit surface via the injected
  fixed-row stubs. No `//go:build integration` tag on any recorder.
- **P0-409-2 (no public-API widening):** honored. The seams
  (`reader`, `freshnessLister`, `driftReporter`) and the
  `newHandlerWith*` constructors are all **unexported**. The exported
  `New(*Store)` / `New(*Store, *Store)` signatures are byte-for-byte
  unchanged; the production wiring in `httpserver.go` is untouched.
- **P0-409-3 (no `_INDEX.md` / `_STATUS.md` edits):** honored. The only
  tracker-adjacent edit is `docs/issues/394-…md` per AC-5 (the slice doc,
  not the tracker), plus the spillover slice docs.

**Outcome:** 5 dashboard panel endpoint pairs covered via Option A (the
exact high-traffic routes the e2e dashboard spec traverses); 3 wider
surfaces deferred via Option C with spillovers. This satisfies AC-2 and is
sufficient to unblock 394 (see D4).

## D2 — Why Option A over Option B for the dashboard panels

Option B (a no-DB degenerate path, like 392's `me` non-UUID-credential
trick) does not apply: none of the five dashboard handlers has a
representative response path that avoids the store call. The store call is
the only data source; there is no synthetic branch. Option A — a one- to
three-method unexported read interface, with `*Store` satisfying it
unchanged — is the minimal faithful seam. It is precisely what slice 392's
metrics-deferral note named as "the proper fix" and keeps the refactor
bounded (no SQL change, no migration, no behavior change — the production
path is identical; only the static type of the handler's field changed
from `*Store` to an interface `*Store` already satisfies).

## D3 — Golden shape: variants (populated + empty), mirroring 392

Each golden is `{_comment, endpoint, variants{}}` with stable
contract-identifier keys (slice 392 D2). Every endpoint records two
variants:

- **`populated`** — a representative non-empty result exercising the
  handler's row→wire mapping (incl. edge cases: the activity golden's
  null-summary row pins `summary: null`; the freshness golden's
  class-less row pins the `"unclassified"` bucket + a stale count).
- **`empty`** — the empty-set result. The BFF must tolerate `[]` with
  `count: 0` (never `null`). This is the load-bearing array-vs-null
  contract the consumer asserts, and the variant a fresh-tenant e2e run
  hits.

Fixture values are obviously-fake (synthetic UUIDs like
`…000000000409`, plain strings) per slice 314 / GitGuardian — no JWT- or
vendor-shaped literals.

## D4 — Drift-sensitivity proof (AC-3)

Proved on the **freshness** endpoint. Renamed the golden's per-bucket
`stale` key → `staleCount`. Result:

- **Provider half failed** (`go test ./internal/api/freshnessdrift/ -run
TestContract_Freshness`): `variant "populated" wire shape drifted from
golden` — the handler emits `stale`, the golden now says `staleCount`.
- **Consumer half failed** (`npm run test -- lib/contracts/freshness.contract.test.ts`):
  `freshness.contract.test.ts:72 — expected "number", received "undefined"`
  (the `typeof b.stale` field-shape assertion).

Both halves red on a single-field rename; golden restored; both green.
This is the slice-210-class catch, reproduced on a dashboard route — the
exact bug class ADR-0007 exists to catch.

## D5 — AC-5: slice 394 reassessment — FLIPPED `blocked` → `ready`

Slice 394 (teach the `/e2e/` `route.fulfill` mocks to load from goldens)
was `blocked` on "golden coverage spanning the high-traffic dashboard
routes the e2e suite actually traverses" (394 Dependencies; 392 D5 reason
1).

I inventoried the routes the e2e specs mock (`grep` over
`web/e2e/*.spec.ts`). The dashboard-panel cluster the e2e suite traverses
is: `/v1/me`, `/v1/evidence/freshness`, `/v1/controls/drift`,
`/v1/upcoming`, `/v1/frameworks/posture`, `/v1/activity`. After this slice,
**every one of those has a golden** (`/v1/me` from 392; the other five from
this slice). Combined with 392's `version` + `install-state` +
`demo/status`, the golden tier now covers **9 endpoints**, and the
dashboard view — 394's stated target — is fully golden-backed.

394's D5-reason-1 deferral ("golden coverage approaches the e2e dashboard
routes") is therefore **resolved for the dashboard view**. A
`fulfillFromGolden` helper built now is no longer premature: it has a
real, contiguous set of dashboard routes to serve. The residual
hand-mocked routes (`/v1/risks`, `/v1/controls/*`, `/v1/board`,
`/v1/policies`) are exactly the per-test-variation escape-hatch case 394's
AC-3 already anticipates — they do not block building the helper for the
golden-backed routes.

**Decision:** flipped `docs/issues/394-…md` Status `blocked` → `ready` and
updated its Dependencies note to record that #409 landed the dashboard
goldens. (Doc edit per AC-5; NOT a `_STATUS.md` edit — the orchestrator
owns that.)

## D6 — Spillovers filed (Option-C deferrals)

Per the continuous-batch spillover policy, the Option-C deferrals are
filed as follow-up slice docs (status `blocked`/`ready` per their deps; no
`_INDEX.md` edit):

- **slice 410** — contract-tier rollout: `GET /v1/risks` (dashboard top-
  risks panel). Needs the risks `Handler` read seam (or a list-only
  sub-interface) AND a transform-aware consumer assert (the BFF re-wraps
  the envelope). `ready`.
- **slice 411** — contract-tier rollout: controls-detail + audit-workspace
  routes. Larger pool-backed surfaces; each needs its own read seam.
  `blocked` on appetite (a v2 quality follow-on; the dashboard-panel core
  is the v1-binary-relevant surface).

## Detection-tier classification (slice 353 Q-13)

- `detection_tier_actual`: `none` — no bug surfaced during the slice; the
  drift-sensitivity injection (D4) was a deliberate proof, not a found
  defect.
- `detection_tier_target`: `contract` — this slice's entire purpose is to
  add the contract tier that would catch the slice-210 class of BFF↔atlas
  shape drift on the dashboard routes at the cheapest surface.
