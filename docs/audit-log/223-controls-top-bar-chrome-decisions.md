# Decisions log — slice 223 (controls top bar chrome parity)

**Slice:** [`docs/issues/223-ui-honesty-controls-top-bar-chrome-parity.md`](../issues/223-ui-honesty-controls-top-bar-chrome-parity.md)
**Type:** JUDGMENT
**Build agent:** Engineer
**Closed at:** 2026-05-24

---

## Summary

Slice 223 was filed against the `/controls` page during slice 204's UI
honesty audit fleet, but the surface it targets — the **shared
authed-shell topbar** — has been load-bearing for several adjacent
slices since the spec was authored:

- **Slice 213** (merged 2026-05-24) already shipped two of the four
  chrome elements the spec named: `<InProgressAuditPill />` + `<UserAvatar />`.
  Both render on `/controls` today via the shared shell.
- **Slice 213 D1** filed two spillover slices — **#271** (breadcrumb)
  + **#272** (global ⌘K search) — for the remaining two elements. #271
  is `ready`; #272 was blocked on backend slice **#268** (unified
  `/v1/search` endpoint).
- **Slice 268** merged 2026-05-24 (commit `d9d8e69b`) — the search
  backend is live on `main`.

So the **actual** missing pieces on `/controls` post-213/214 are only
the breadcrumb + the global search. The audit-pill + avatar are no-
ops because they already exist in the shared shell.

This log captures the JUDGMENT calls the engineer made while
building the slice. The slice spec recorded a single decision slot
(D1 — subset shipped); five additional engineer-side calls were made
during implementation and are recorded here for traceability.

---

## D1 — Subset shipped: breadcrumb + global ⌘K search (BOTH); spillovers 271 + 272 SUPERSEDED

**Decision:** ship **both** missing chrome elements in this slice and
mark spillover slices **#271** + **#272** as **superseded** by 223
(rather than blocking on them).

**Rationale (why ship both vs defer one):**

| Element              | Current state                              | Ship in 223? | Reason                                                                                                                                                                                                                                       |
| -------------------- | ------------------------------------------ | ------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| In-progress pill     | already shipped (slice 213)                | n/a          | already in shared shell; no-op for 223                                                                                                                                                                                                       |
| User avatar          | already shipped (slice 213)                | n/a          | already in shared shell; no-op for 223                                                                                                                                                                                                       |
| Breadcrumb           | not shipped; spillover 271 ready           | YES          | Narrow scope (~150 LOC including the pure helper + test). Backing data is `/api/me/tenants` (slice 192) — no new endpoint, no new wire shape. Pure helper `derivePageName` is unit-coverable; integrated render is e2e-coverable.            |
| Global ⌘K search     | not shipped; spillover 272 unblocked by 268 | YES          | Backend slice 268 just merged with the exact `GET /v1/search?q=...` shape the spec assumed (AC-2). The BFF + UI is one self-contained piece (~250 LOC). Spillover 272 would have re-implemented the same surface as a separate PR — same code, more rebases. |

**Architectural reason to consolidate:** all four chrome elements
touch the same shared shell (`web/components/shell/topbar.tsx`).
Three slices (223 + 271 + 272) racing for the same file would force
three rebases against `main` and triple the review surface for the
same net code. The maintainer pattern for this is "consolidate when
the dependency graph collapses" (canvas §1.6 anti-pattern: don't
ship three slices to fix one chrome surface).

**Spillover slices superseded:**

- [`271-shared-shell-breadcrumb.md`](../issues/271-shared-shell-breadcrumb.md) — `ready`. Same component, same data source, same AC-3/4 obligation. The orchestrator should mark slice 271 as `superseded-by 223` in `_STATUS.md` on merge.
- [`272-global-search-cmdk.md`](../issues/272-global-search-cmdk.md) — `not-ready` (now unblocked by 268). Same component shape, same `/v1/search` backend. The orchestrator should mark slice 272 as `superseded-by 223` in `_STATUS.md` on merge.

**Trade-off accepted:** slice 223's spec recommended a "popover below
the input" UX shape for results (AC-3); spillover 272 documented a
"cmd-K modal" UX shape (AC-2). Per AC-3 the spec is the authority —
I shipped the **popover** below the input, not a modal. Future
slices may re-evaluate if operator feedback prefers the modal pattern
(Linear / Stripe / Vercel use the modal; security-atlas's mockup at
`Plans/mockups/controls.html` shows the popover).

**What this resolves:** the slice spec is silent on whether to
consolidate with the spillovers; D1 was explicitly maintainer
JUDGMENT. The dependency-graph collapse argument resolves it.

---

## D2 — Breadcrumb is a client component (not server)

**Decision:** `<Breadcrumb />` is a **client component** that uses
`usePathname()` for the right-hand label + a `useEffect` fetch of
`/api/me/tenants` for the left-hand tenant name.

**Rationale:**

- The breadcrumb's right segment is **route-derived** — Next.js App
  Router exposes the pathname to client components via
  `usePathname()`. A server component would have to receive the
  pathname via a proxy-injected header (e.g. `x-pathname`) — adding
  a header pipeline for one read is more rope than a client
  component.
- The left-hand tenant fetch is the same `/api/me/tenants` source
  the `<TenantSwitcher />` already consumes; a small client-side
  fetch (no periodic re-fetch — the tenant rarely changes
  in-session) keeps the rendering surface symmetric with the
  switcher.
- The pure derivation helpers (`derivePageName`, `pickCurrentTenantName`)
  are extracted to module-level functions and unit-tested directly
  (slice 069 P0-A3 — vitest is node-env, no JSX rendering harness).

**What this resolves:** spec did not specify server-vs-client. The
client + pure-helper choice is the cheapest architecture for the data
flow.

---

## D3 — Search results render in a popover (not a modal)

**Decision:** `<GlobalSearch />` results render in a **popover below
the input**, NOT a centered modal.

**Rationale:**

- The spec AC-3 explicitly names "popover below the input (shadcn/ui
  `<Command>` pattern)". The mockup at `Plans/mockups/controls.html`
  shows the input inline in the topbar, not a modal trigger.
- The popover is cheaper to ship — no `<Dialog>` import, no focus-
  trap library, no scroll-lock. The slice 069 anti-criterion against
  over-engineering applies.
- Spillover 272's spec (AC-2) named the modal pattern; the slice 223
  spec is the authority since this slice closes both. A future slice
  can promote to a modal if operator feedback warrants — the result
  rows + keyboard nav are already factored.

**Trade-off accepted:** the popover is a tighter UX surface than the
modal. Operators on /controls with a small viewport may see results
overlap page content. Acceptable for v1 — the input + results are
right-aligned and the popover's right edge anchors to the input.

**What this resolves:** spec called for a popover (AC-3); spillover
272 spec called for a modal (272 AC-2). 223 spec wins.

---

## D4 — Result routing: controls→detail, risks→hierarchy?focus, evidence→list

**Decision:** the popover row routes follow the established
per-primitive conventions:

| Type     | Route                                | Why                                                                                                          |
| -------- | ------------------------------------ | ------------------------------------------------------------------------------------------------------------ |
| controls | `/controls/${id}`                    | The slice-041 detail page exists                                                                             |
| risks    | `/risks/hierarchy?focus=${id}`       | No per-risk detail page on main; slice 100 set the convention of deep-linking into the hierarchy view        |
| evidence | `/evidence`                          | No per-evidence detail page on main; the list page is the honest destination (slice 098 P0-A1: no fabrication) |

**Rationale:** AC-3 says "Each result links to the entity's detail
page." When the detail page doesn't yet exist, the slice-100 risks
page already established the convention of `?focus=<id>` deep-linking
into the hierarchy view as the closest-honest destination. Evidence
has no per-row detail anywhere on main — the list page is the only
honest destination.

**What this resolves:** AC-3 was silent on routing-when-no-detail-page.
The convention chain is the constitutional move.

---

## D5 — Debounced fetch via cascading setTimeouts (lint compliance)

**Decision:** the search component's debounced fetch lives in a
`useEffect` that wraps EVERY setState call in a `setTimeout(0)` or
`setTimeout(250)` external subscription — no synchronous setState in
the effect body.

**Rationale:** the `react-hooks/set-state-in-effect` lint rule (slice
192 + 213 also navigate this) rejects synchronous setState in the
effect body. The pattern slice 192's `TenantSwitcher` settled on is
`queueMicrotask(fetchTenants)`; I used `setTimeout(0)` for the
below-min-length branch (functionally identical) and a `setTimeout(250)`
for the actual debounced fetch. Both are external subscriptions per
the rule's documentation.

**What this resolves:** lint compliance with a load-bearing React
hooks rule. The rule documentation explicitly endorses external
subscriptions (setTimeout, setInterval) as the escape hatch.

---

## D6 — Fixture seeds a `tenants` row for the demo tenant UUID

**Decision:** add `fixtures/e2e/controls-top-bar.sql` that INSERTs a
`tenants` row for the demo tenant UUID (`d3a0…`) with name
`"Demo Tenant"`.

**Rationale:** the `/v1/me/tenants` handler (slice 192) joins the
JWT's `available_tenants[]` claim against the slice-144 `tenants`
table for the human-readable name. The bootstrap seed
(`deploy/docker/bootstrap/seed.sql`) inserts `"Default Tenant"` for
a DIFFERENT canonical UUID (`000-0000-4000-8000-0001`); the Playwright
harness's `DEMO_TENANT_ID` is `d3a0…` (`web/e2e/seed.ts`) and has no
bootstrap row by default in CI.

Without this seed, the breadcrumb's left segment resolves to empty
string and `pickCurrentTenantName` returns null (whitespace-only
trim contract) — the breadcrumb chrome stays null and the AC-7
assertion fails.

The "Demo Tenant" name is benign — no PII, no maintainer-identifying
string, matches the `demo-*` naming used elsewhere in the harness.

**What this resolves:** the e2e environment's lack of a default
tenant row for the demo UUID. The fixture is the minimal surface to
close the gap without expanding the seed harness's overall
responsibilities.

---

## D7 — No changes to `_STATUS.md` (spec hard rule)

**Decision:** this slice does NOT modify `docs/issues/_STATUS.md`.
The loop orchestrator owns canonical row flips, including the
`superseded-by` marker for slices 271 + 272.

**Rationale:** spec hard rule. The decisions log + PR body name the
spillovers explicitly so the orchestrator has the information to
flip the rows.

---

## CI-delta scan

No CI-delta concerns:

- **New files only** (no modifications to load-bearing existing files
  except `web/components/shell/topbar.tsx` and `web/e2e/seed.ts`):
  - `web/components/shell/breadcrumb.tsx` + `.test.ts`
  - `web/components/shell/global-search.tsx` + `.test.ts`
  - `web/lib/page-names.ts` + `.test.ts`
  - `web/app/api/search/route.ts` + `.test.ts`
  - `web/e2e/controls-top-bar.spec.ts`
  - `fixtures/e2e/controls-top-bar.sql`
  - `docs/audit-log/223-controls-top-bar-chrome-decisions.md`
- **`web/components/shell/topbar.tsx`** modified to mount the two
  new components. The mount points are additive — no existing
  affordance is moved or removed. The header comment is extended
  with a slice 223 block.
- **`web/e2e/seed.ts`** extended with the `"controls-top-bar"`
  literal in the `FixtureName` union (additive; no rename, no
  removal). Mirrors slice 213's identical extension.

Local CI parity verified before push:

- `pre-commit run --all-files` — green
- `npm run lint` (web) — green (2 pre-existing warnings in
  `scripts/capture-readme-screenshots.ts`; identical baseline to
  slice 213's merge)
- `npm run test` (web) — 99 files / 1052 tests / all green (incl.
  the 25 new `page-names.test.ts` tests + the 6 new
  `breadcrumb.test.ts` tests + the 14 new `global-search.test.ts`
  tests + the 6 new `search/route.test.ts` tests = 51 new tests)
- `npx tsc --noEmit` (web) — 15 errors, identical baseline to main
  (pre-existing in `scripts/capture-readme-screenshots.test.ts`,
  `lib/auth/oauth-client.test.ts`, `next-config.test.ts`); no new
  errors from this slice
- `npm run build` (web) — green; all 35+ routes compile cleanly
- CHANGELOG bullet added under `## [Unreleased]` → `### Added`

The Playwright e2e spec (`controls-top-bar.spec.ts`) requires the
slice-082 seed harness + a running platform; it is exercised in CI
by the `Frontend · Playwright e2e` job after the docker-compose
bring-up. The slice 274 AC-9 flake fix (merged 2026-05-23) keeps the
spec reliable in CI.
