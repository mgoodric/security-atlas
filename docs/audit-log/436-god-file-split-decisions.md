# Slice 436 — split the three oversized hand-written god-files (decisions log)

**Type:** JUDGMENT (behavior-preserving structural refactor — no sign-off gate).

- detection_tier_actual: unit
- detection_tier_target: unit

A behavior-preserving split has one real failure mode: a route / testid / ARIA
hook silently dropped or relocated. Two real defects surfaced DURING the slice,
both in the mechanical-extraction tooling (not the shipped code), and both were
caught at the cheapest tier:

1. A duplicate `return root` left in `buildRouter()` by the extraction script —
   caught by `go vet` ("unreachable code") in the local unit-tier run, fixed
   before any commit.
2. An off-by-one in the first frontend extraction (a section range cut a
   component mid-body) — caught immediately by `tsc --noEmit` (syntax error)
   in the local typecheck, fixed by re-deriving boundaries from the original
   file's section-divider comments.

Both `actual` and `target` are `unit` (the route-walk Go test + `tsc`/`eslint`
are the unit-tier surfaces that caught the tooling slips). No defect escaped to
integration, e2e, or production. The new route-walk test is itself the durable
unit-tier guard that makes the backend split provably lossless on every future
PR.

---

## Decisions made

### D1 — Backend: per-domain `register_*.go` in the same `package api`, called from a new `buildRouter()` seam

**Options:** (a) leave `httpHandler()` monolithic and only split the frontend;
(b) move route registration to a new sub-package; (c) extract per-domain
registrar methods into `register_*.go` files in the SAME `package api`, called
in original order from a thin `buildRouter()`.

**Chosen:** (c). The slice spec mandates same package, same exported symbols, no
caller change. `httpHandler()` now does exactly one new thing — call
`s.buildRouter()` and wrap the result in the existing otelhttp layer — and
`buildRouter()` wires the shared middleware once, constructs the cross-cutting
shared stores, and calls 17 per-domain registrars in the original declaration
order. `httpserver.go` dropped from 1753 → 609 LOC; the largest extracted file
is `register_admin.go` at 265 LOC.

**Rationale:** mirrors the slice 370/396 `web/lib/api.ts` → `web/lib/api/*.ts`
per-domain precedent applied to the Go side. Same-package methods keep every
exported symbol and every `cmd/atlas` call site unchanged (P0 from the spec).

### D2 — Registrars called in strict original file order (chi declaration-order safety)

**The hazard:** chi resolves same-method literal-vs-param routes in declaration
order (the inline code is littered with "literal-segment route declared BEFORE
`/{id}` so chi's declaration-order match keeps it ahead" comments). Reordering
registration would silently break routing even when the route SET is identical.

**Chosen:** each registrar covers a CONTIGUOUS line range of the original
`buildRouter()` body, and `buildRouter()` calls them in the same order the inline
blocks ran. Each domain's literal-before-param ordering is preserved verbatim
inside its registrar. Cross-domain `/v1/controls/...` and `/v1/risks/...`
sub-routes were already registered in distinct, non-interleaved blocks, so
contiguous extraction preserves every relative order.

**Rationale:** the AC-5 route-walk test asserts the route SET, but order is a
separate correctness property; keeping registrars contiguous-and-in-order makes
order-preservation structural rather than something the test has to prove.

### D3 — Cross-cutting shared stores constructed once in `buildRouter()`, threaded into the registrars that share them

**Chosen:** `featureFlagStore` (oscal-gate + board-gate + admin features),
`risksStore` (risk routes + dashboard-export panel source), `vendorStore`
(vendor routes + board-pack burndown adapter), and `freshnessStore` /
`driftStore` (freshness/drift + dashboard + board reads) are built once in
`buildRouter()` and passed as parameters. Domain-local stores stay inside their
registrar.

**Rationale:** preserves single-resource identity exactly as the inline code
had it (one `risk.Store` backing both the risk routes and the dashboard export;
one `vendor.Store` behind both vendor routes and the board adapter). Passing the
shared store down is the minimal change; re-constructing per registrar would
have been a behavior change (two stores where there was one).

### D4 — AC-6 proven by a per-route middleware-DEPTH golden, not by re-reading the auth wiring

**The risk (spec P0):** moving an admin/feature-gated route out from under its
group middleware. **Chosen:** the route-walk test records, per route, the depth
of the chi inline-middleware chain and asserts it against a golden captured from
clean main. The shared auth/tenancy/authz chain is applied via `root.Use()` to
the SINGLE `root` router every registrar receives, so the base depth (8) is
identical for every route; routes inside a `featureflag.Gate` group
(`oscal.export`, `board.reporting`) or the SCIM auth subrouter carry depth 9.
The golden pins exactly which 36 routes carry the extra group middleware —
and they are precisely the oscal-components, board (briefs/packs/narrative), and
`/scim/v2/*` routes, nothing else.

**Why no route changed its gate:** the global auth chain is `root.Use()`-applied
once before any registrar runs, so a registrar physically CANNOT register a
route onto an un-gated mux — there is only one `root`, and it is already gated.
The only group-level middleware (the two feature gates + the SCIM auth
subrouter) was moved verbatim, intact, inside its registrar (`registerOSCAL`,
`registerBoard`, `registerAdmin`), so the `chi.Group` boundary — and thus the
depth-9 set — is byte-for-byte preserved. The middleware-depth golden is the
mechanical proof.

### D5 — Settings page → `_tabs/` per-section components; control-detail → `_sections/` (panels + bodies)

**Settings (2152 → 118 LOC):** the page already composed six top-level section
components (`ProfileSection`, `TenantSection`, `AppearanceSection`,
`NotificationsSection`, `ApiTokensSection`, `SessionsSection`). Each section +
its private sub-components and BFF wrappers moved into `_tabs/<name>.tsx`,
exporting the section component; the page is now a thin shell that fetches the
admin flag and composes the six sections — identical JSX, identical props.

**Control-detail (1472 → 772 LOC):** the per-tab panel components
(`OverviewPanel` … `HistoryPanel`) and the leaf `*Body` components +
date/source formatters were the natural section boundary. They moved into
`_sections/panels.tsx` (the 7 panels) and `_sections/bodies.tsx` (the shared
leaf bodies the OverviewPanel and the per-tab panels both render). The page
KEEPS `ControlDetailPageInner` (which owns ALL the React Query state + tab
routing + loading/error branches) and passes data down as props — exactly the
spec's "keep shared state lifted in the page shell" guidance. No state-management
refactor was forced, so no deferral (AC-10) was needed.

**`_sections/` vs `_tabs/` naming:** settings uses `_tabs/` (the spec's named
convention; the sections render as a stacked tab-like list). Control-detail uses
`_sections/` because its extracted units are a mix of tab-panels AND the shared
body components they compose — `_sections` reads truer than `_tabs` for a folder
that holds both. Both are Next.js underscore-prefixed private folders (not
routes); the Next build confirms neither becomes a route.

### D6 — Control-detail bodies split out separately so the 710-line panel cluster doesn't become a new ~700-line file

**Chosen:** rather than one ~710-line `_sections/panels.tsx`, the tightly-coupled
leaf `*Body` components (shared by OverviewPanel and the per-tab panels) live in
`_sections/bodies.tsx` (411 LOC) and the 7 panels in `_sections/panels.tsx`
(443 LOC), with panels importing the bodies. This keeps every extracted file
well under half the original 1472 (AC-2).

### D7 — Import pruning done mechanically from eslint's own unused-vars report

Each extracted frontend file was seeded with the original file's full import
block, then pruned by parsing eslint's `@typescript-eslint/no-unused-vars`
output and removing exactly the flagged named members (dropping whole import
statements when every member was unused), followed by prettier. Result: 0 tsc
errors, 0 eslint warnings across the touched dirs. This avoided hand-curating
six-plus import lists (the error-prone path) while guaranteeing no
behaviorally-used import was dropped (tsc would have failed).

---

## Revisit once in use

- **Route-walk golden maintenance.** `internal/api/testdata/routes.golden` (274
  routes) and `routes_mw.golden` are deliberately NOT auto-rewritten. When a
  future slice legitimately adds/removes a route, regenerate both in the SAME PR
  and eyeball the diff — that is the intended workflow, and the diff is the
  review artifact. If the regeneration ever feels like friction, that is the
  signal to add a `-update` flag to the test (don't pre-build it now).
- **Route-walk coverage of dep-gated routes.** The test wires every optional
  dependency with a non-dialing pool so the maximal route surface mounts. If a
  future dep's constructor starts dialing at construction time (today none do),
  the test's `routeWalkServer` helper will need that constructor stubbed — the
  failure will be loud (a panic in the helper), not silent.
- **Frontend `_tabs/` / `_sections/` re-accretion.** There is no `max-lines`
  ESLint cap on `app/**` pages (only on `lib/api/**`). If the settings or
  control-detail sections start re-growing past ~600 LOC, consider extending the
  slice-370 `max-lines` rule to these folders. Not done now — premature.
- **Settings `_tabs/` are sections, not real tabs.** They render as a stacked
  list today. If a future slice makes settings genuinely tabbed, the `_tabs/`
  folder name will finally be literal; no rename needed in the interim.

---

## Confidence

| Decision                                         | Confidence |
| ------------------------------------------------ | ---------- |
| D1 — `register_*.go` same-package seam           | high       |
| D2 — strict original-order registrar calls       | high       |
| D3 — shared stores threaded from `buildRouter()` | high       |
| D4 — AC-6 via middleware-depth golden            | high       |
| D5 — `_tabs/` + `_sections/` page split          | high       |
| D6 — bodies split from panels                    | high       |
| D7 — mechanical import pruning                   | high       |

Every decision is `high` confidence: the route-walk test (backend) and the
testid/ARIA/id set-equality checks plus the e2e suite (frontend) are objective
behavior-preservation oracles, not judgment calls. The only genuinely subjective
choices were the folder naming (D5) and the panels-vs-bodies cut (D6), both
cosmetic and reversible.
