# 204 — Page audit: /risks (list view)

**Audit run:** 2026-05-23
**Live URL:** `https://atlas-edge.home.gmoney.sh/risks`
**HTTP status:** `200 OK`
**Mockup:** `Plans/mockups/risks.html`
**Page slug:** `risks`
**Page implementation:** `web/app/(authed)/risks/page.tsx`
**Filter module:** `web/app/(authed)/risks/filters.ts`
**BFF:** `web/app/api/risks/route.ts` → upstream `GET /v1/risks`
**Auth:** admin JWT (atlas_jwt cookie). Read-only audit; no clicks on mutating affordances.

## Parent slice

#204 — Comprehensive page-by-page UI parity audit (per-page agent fleet). Each finding below files as its own spillover slice per AC-2 / P0-A7.

## What the live page does today (factual baseline)

- Renders the slice 100 list view: server component skeleton + client `RisksPageInner` (`useQuery` keyed `["risks","list"]`, hits BFF `/api/risks`).
- BFF proxies the bearer cookie to upstream `GET /v1/risks`. Upstream on this deployment returns `{"count":0,"risks":[]}` — the bootstrap seed ships an empty register, so the page renders the slice-152 zero-state with the `Add first risk` CTA routing to `/risks/new` (slice 105).
- Filter pills: **Treatment**, **Severity**, **Owner** (three pills). Slice 100's `filters.ts` comment explicitly notes the AC narrowed the shipped pill set to three even though the data carries the other columns.
- Table columns (in the populated path): ID · Title · Category · Treatment · Owner · Residual · Severity · Review due · **per-row "View in hierarchy →" link** (eight data columns + one actions column).
- Slice 185 changes verified: page banner `data-testid="risks-detail-future-slice-banner"` reads "Per-risk detail page is a future slice"; `onRowClick` removed; per-row hierarchy-link present at `data-testid="risks-row-hierarchy-link"`.
- Header actions: `Hierarchy view →` link + Export CSV / JSON / XLSX (slice 136 wires three buttons to `/api/risks/export?format=...`) + a primary `New risk` button rendered with `disabled` (no tooltip, no banner explaining the state).
- Subtitle reads: "Flat list of all risks · for the org-tree view see Risk hierarchy" (no above-appetite tally; see finding 3).

## What the mockup promises (factual baseline)

- Top bar with: tenant breadcrumb (`Sentinel Labs > Risks`), in-bar search (`Search controls, evidence, risks…` with `⌘K` kbd hint), SOC 2 Type II audit-in-progress banner pill, user avatar (`MG / Sam`).
- Title row: `Risk register` h1 + subtitle `47 risks · 3 above appetite` + sub-subtitle "Flat list of all risks · for the org-tree view see Risks → Hierarchy".
- Filter pills: **Category** (Operational / Compliance / Third-party / Strategic), **Treatment** (mitigate / transfer / accept / avoid), **Methodology** (nist_800_30 / fair / five_by_five), **Org unit** (Platform / Customer success / Corporate IT), **Owner** (five pills — Category, Methodology, Org unit are missing from live; live's Severity pill is not in the mockup).
- Two action buttons: `Export CSV` (one) + `New risk` (live ships three export buttons + a disabled `New risk`).
- Footer pagination row: "Showing 1–7 of 47" + Previous (disabled) + Next buttons.

## Findings (one slice per finding)

| #   | Category                        | Severity | Spillover | Subject                                                                               |
| --- | ------------------------------- | -------- | --------- | ------------------------------------------------------------------------------------- |
| 1   | (i) Layout / chrome parity      | medium   | #243      | Top bar omits tenant breadcrumb, search input, audit-in-progress banner, user avatar  |
| 2   | (i) Layout / chrome parity      | medium   | #244      | Filter pills incomplete — Category, Methodology, Org unit missing; data already wired |
| 3   | (iv) Mockup-stale               | low      | #245      | Mockup subtitle "N above appetite" — risk appetite is not a v1 backend concept        |
| 4   | (i) Layout / chrome parity      | low      | #246      | Pagination control absent — table renders all rows in a single page with no Prev/Next |
| 5   | (ii) Broken / silent affordance | medium   | #247      | Header "New risk" button is silently disabled — `/risks/new` is a shipped route       |

No category (iii) findings. The live page's data-bound surfaces are honest: with zero risks in the bootstrap seed, the `Showing 0 of 0 risks` meta, the `No risks logged yet` empty-state, and the `Add first risk` CTA routing to `/risks/new` are all truthful. No fabricated counts, no fake severity badges.

## Detailed findings

### Finding 1 — Top bar omits tenant breadcrumb, search, audit banner, user avatar (#243)

**Category:** (i) layout / chrome parity
**Severity:** medium — same horizontal chrome gap previously flagged on `/controls` (#223), `/audits` (#213), `/dashboard` (#228). The risks page inherits the authed-layout header at `web/app/(authed)/layout.tsx`, so the gap is identical: the mockup signals a wayfinding-rich top bar; the live header has only the logo + `Sign out`.

**What the mockup shows:**

- Breadcrumb chip: `Sentinel Labs > Risks` (tenant context in the chrome, mockup lines 32-36).
- Search input with placeholder `Search controls, evidence, risks…` and `⌘K` kbd hint (mockup lines 42-46).
- Audit-in-progress pill: amber background, pulsing dot, text `SOC 2 Type II · Q2 2026 in progress` (mockup lines 38-41).
- User avatar circle (initials) + display name (mockup lines 47-50).

**What the live page shows:**

- Logo + `security-atlas` wordmark + `v0 · self-host` chip.
- `Sign out` button (right-aligned).
- TenantSwitcher renders but is a switcher, not a breadcrumb chip.

**Evidence:**

- `Plans/mockups/risks.html` lines 23-53 (top-bar `<header>` block).
- Live HTML at `/risks`: `<header class="flex h-14 shrink-0 items-center justify-between border-b bg-background px-6">` — no `<input type="search">`, no `Sentinel Labs` text, no SOC 2 banner pill.

### Finding 2 — Filter pills incomplete: Category, Methodology, Org unit missing (#244)

**Category:** (i) layout / chrome parity (the missing data is wired through the Risk type already — this is a pure UI gap)
**Severity:** medium — Methodology is load-bearing per canvas §6 (risk methodology default is an open question; once a tenant picks one, surfacing it in the filter set is how you slice the register). Category is the natural top-level cut. Org unit is how a security leader scopes the register to a team or BU.

**What the mockup shows:** Five pills — Category, Treatment, Methodology, Org unit, Owner (mockup lines 126-173).

**What the live page shows:** Three pills — Treatment, Severity, Owner. (`web/app/(authed)/risks/filters.ts` lines 53-57: `RiskFilters` carries only those three keys.)

The live page's Severity pill is a meaningful addition not present in the mockup — that's a net positive, not a gap. The gap is the three missing pills.

The slice-100 module comment at `filters.ts` lines 11-14 documents the gap explicitly:

> "Filter set per AC-3: treatment + severity band + owner. The mockup shows category/methodology/org_unit pills too, but the AC narrows the shipped pill set to three. Adding the rest is a future extension; the data is on `riskWire` already so the cost is purely UI."

The `Risk` type at `web/lib/api.ts` lines 1952-1973 confirms `category: string`, `methodology: string`, `org_unit_id?: string` are all present on the wire. Spillover work is presentational only: extend `RiskFilters`, add three pill definitions to `pills`, add three corresponding entries to `applyFilters`.

**Evidence:**

- `Plans/mockups/risks.html` lines 126-173.
- `web/app/(authed)/risks/filters.ts` lines 11-14 (acknowledging-comment), 53-57 (`RiskFilters` type).
- `web/lib/api.ts` lines 1952-1973 (`Risk` type carries the missing fields).
- Live HTML: extracted-text body shows `Treatment` / `Severity` / `Owner` pills; no `Category` / `Methodology` / `Org unit`.

### Finding 3 — Mockup-stale: "N above appetite" subtitle has no backend concept (#245)

**Category:** (iv) mockup-stale
**Severity:** low — the mockup carries it as a 1-line factoid in the title row; the live page omits it honestly. The fix is to update the mockup, not to ship an appetite field.

**What the mockup shows:** Title row subtitle reads `47 risks · 3 above appetite` (mockup line 111).

**What the live page shows:** No "above appetite" count anywhere. The subtitle in `RisksPageInner` (`page.tsx` lines 353-360) reads only "Flat list of all risks · for the org-tree view see Risk hierarchy".

**Why this is mockup-stale, not a gap:** `Plans/canvas/06-risk.md` does NOT mention `appetite`, `risk_appetite`, or `above_appetite`. The canvas's residual-calculation flow (canvas §6: `Residual = inherent × (1 − control_effectiveness)`) has no appetite layer. Grepping `internal/risk/`, `migrations/`, `web/lib/`, the schema, and the wire types for the substring `appetite` returns zero matches outside the mockup file. There is no field to surface, no API to wire — the mockup is referencing a concept that does not exist in v1.

If risk appetite is desired (it's a common GRC concept and would slot in alongside the methodology choice), a v2+ slice would (a) add an appetite-band column or per-category appetite policy, (b) compute the per-risk over-appetite predicate, (c) surface the tally. That's product scope, not a parity gap. The smaller correct path for now: walk the mockup back.

**Evidence:**

- `Plans/mockups/risks.html` line 111 (`47 risks · 3 above appetite`).
- `Plans/canvas/06-risk.md` (no `appetite` references).
- `web/lib/api.ts` lines 1952-1973 (`Risk` type — no appetite field).
- Live HTML title-row text from extraction: no appetite tally.

### Finding 4 — Pagination control absent (#246)

**Category:** (i) layout / chrome parity
**Severity:** low — at 0 risks today the absence is invisible; once a register grows past ~50 rows the unpaginated table becomes a usability issue. Same pattern as `/controls` finding 5 (#227).

**What the mockup shows:** Footer row "Showing 1–7 of 47" + Previous (disabled) + Next buttons (mockup lines 267-273).

**What the live page shows:** No pagination UI. `<ListTable>` renders every row from `visible` (the full filtered set). No `?page=` query param, no `LIMIT`/`OFFSET` plumbing to upstream.

**Evidence:**

- `Plans/mockups/risks.html` lines 267-273.
- `web/app/(authed)/risks/page.tsx` lines 469-474 — `<ListTable>` consumes `visible` directly with no pagination state.
- Same `web/components/list/list-table.tsx` shell that #227 noted has no pagination prop.

### Finding 5 — Header "New risk" button is silently disabled (#247)

**Category:** (ii) broken interaction / silent affordance
**Severity:** medium — same slice-178 honesty-heuristic pattern as `/controls` #225: a primary-styled, full-visual-weight button rendered with `disabled=""` and no tooltip / banner / explanation. Click → nothing.

The honesty gap is especially sharp here because `/risks/new` IS a shipped route (slice 105, with the slice-151 follow-on tracking the control-multi-select gap). The page already routes the empty-state CTA (`Add first risk` at `page.tsx` lines 420-428) to `/risks/new`. The header button could simply be a `<Link href="/risks/new">` and the affordance would work.

**What the mockup shows:** Primary-styled enabled `New risk` button (mockup lines 118-121).

**What the live page shows:** `<Button size="sm" disabled>New risk</Button>` (`page.tsx` line 347) — primary-styled, disabled at the DOM level, no `title=`, no adjacent banner, no "coming in slice #N" hint.

The dichotomy with the empty-state CTA is the smoking gun: the page knows `/risks/new` is the right destination (it routes the empty-state button there), but the header button is hardcoded disabled. There is no good reason for this asymmetry; the slice-105 follow-on gaps (slice 151) are separately tracked.

**Evidence:**

- `Plans/mockups/risks.html` lines 118-121.
- `web/app/(authed)/risks/page.tsx` line 347 (`<Button size="sm" disabled>New risk</Button>`).
- `web/app/(authed)/risks/page.tsx` lines 420-428 (empty-state CTA already routes to `/risks/new`).
- `web/app/(authed)/risks/new/page.tsx` exists; slice 105 merged.

## Screenshots / DOM dumps

No screenshots committed (per slice 204 AC-7 / P0-A5 anti-criterion — admin-JWT HTML is scrubbed; no `Bearer` substring, no `atlas_jwt=` cookie value, no real-tenant content in this file).

## Notes for the maintainer

- Findings 1 + 4 repeat horizontal patterns previously flagged on `/controls`, `/audits`, `/dashboard`. If the layout-shell work bundles top-bar parity + pagination chrome into one cross-page slice, slices #243 and #246 collapse into that bundle. The per-page spillover discipline keeps the audit traceable; the maintainer can consolidate at execution time.
- Finding 2 is the largest material gap on this page — three filter pills missing where the data is already wired. Slice 100's own module comment names the gap; the spillover just executes the documented extension.
- Finding 3 is the only category (iv) on the page. The remaining 4 findings are real implementation gaps.
- Finding 5 is the most honesty-flavored: the page literally routes the empty-state CTA to the destination the header button refuses to navigate to. That's an internal contradiction the slice-185 honesty pass missed.
- The atlas-edge instance has 0 risks (`{"count":0,"risks":[]}` from `GET /v1/risks`). The audit therefore exercises the empty-state path; the table-shape findings are mockup-vs-implementation reads, not "what does it look like with rows" reads. Slice 100 includes vitest coverage of the populated table; the table shape findings (column set, row presentation) are validated against the page implementation rather than the live render.
