# 204 — Page audit: /controls (list view)

**Audit run:** 2026-05-23
**Live URL:** `https://atlas-edge.home.gmoney.sh/controls`
**HTTP status:** `200 OK`
**Mockup:** `Plans/mockups/controls.html`
**Page slug:** `controls`
**Page implementation:** `web/app/(authed)/controls/page.tsx`
**BFF:** `web/app/api/controls/route.ts` → upstream `GET /v1/anchors?include=state`
**Auth:** admin JWT (atlas_jwt cookie). Read-only audit; no clicks on mutating affordances.

## Parent slice

#204 — Comprehensive page-by-page UI parity audit (per-page agent fleet). Each finding below files as its own spillover slice per AC-2 / P0-A7.

## What the live page does today (factual baseline)

- Renders the slice 098/104 list view: server component skeleton + client `ControlsPageInner` (`useQuery` keyed `["controls","list"]`, hits BFF `/api/controls`).
- BFF proxies the bearer cookie to upstream `GET /v1/anchors?include=state`. Upstream returns 53 SCF anchors (the bootstrap-seed SCF catalog import on atlas-edge), each with a null `state` cell (no tenant has instantiated controls against any anchor in this deployment).
- Filter pills: Framework, Family, State, Freshness (four pills).
- Table columns: SCF anchor · Name · Family · State · Freshness · Last observed (six columns).
- Empty-state branches present per slice 152 (truly-empty vs filter-narrowed).
- Export affordances: Export CSV / JSON / XLSX + Export History CSV / JSON / XLSX + New control (six export buttons + one disabled button).

## What the mockup promises (factual baseline)

- Top bar with: tenant breadcrumb (`Sentinel Labs > Controls`), in-bar search (`Search controls, evidence, risks…` with `⌘K` kbd hint), SOC 2 Type II audit-in-progress banner pill, and a user avatar (`MG / Sam`).
- Title row: `Controls` h1 + subtitle "82 controls · 6 frameworks in scope" + sub-subtitle "SCF anchors × framework satisfactions · evaluated against live evidence".
- Filter pills: Framework, Family, State, Freshness, **Scope** (five pills — Scope is the fifth, with options `env=prod`, `cloud=aws`, `bu=platform`).
- Filter-row right-aligned meta: "Showing 82 of 1,247 SCF anchors" (live: "Showing N of M").
- Table columns: SCF anchor · Name · Family · State · Freshness · Last observed · **Frameworks** (seven columns — Frameworks is the right-aligned column showing satisfactions like `SOC2 · ISO · CSF`).
- Two action buttons: "Export CSV" + "New control" (live ships SIX export buttons).
- Footer row: "Showing 1–7 of 82" + Previous / Next pagination buttons.

## Findings (one slice per finding)

| #   | Category                        | Severity | Spillover | Subject                                                                                  |
| --- | ------------------------------- | -------- | --------- | ---------------------------------------------------------------------------------------- |
| 1   | (i) Layout / chrome parity      | medium   | #223      | Top bar omits tenant breadcrumb, search input, audit-in-progress banner, user avatar     |
| 2   | (i) Layout / chrome parity      | medium   | #224      | Scope filter pill (env / cloud / bu) absent from controls list                           |
| 3   | (ii) Broken / silent affordance | medium   | #225      | "New control" button is silently disabled — no explanatory tooltip or banner             |
| 4   | (i) Layout / chrome parity      | low      | #226      | Frameworks-per-row column missing; per-anchor satisfaction set is invisible on the list  |
| 5   | (i) Layout / chrome parity      | low      | #227      | Pagination control absent — table renders all rows in a single page with no Prev/Next UI |

No category (iii) findings (the live page's data-bound surfaces — anchor count, family list, state cells — match what the upstream `/v1/anchors?include=state` returns honestly; "Showing 53 of 53" is true, the `—` state cells are true).

No category (iv) findings either, in the sense of "mockup references a feature that has no backing implementation". The mockup's Scope filter is the closest candidate — but the _concept_ of scope is real (canvas §5; FrameworkScope is a load-bearing invariant). What's missing is the _filter affordance_ on this list view; treating that as (i) parity rather than (iv) staleness because Scope is unambiguously a v1 primitive.

## Detailed findings

### Finding 1 — Top bar omits tenant breadcrumb, search, audit banner, user avatar (#223)

**Category:** (i) layout / chrome parity
**Severity:** medium — the missing affordances are user-facing wayfinding, not strictly broken interactions. The search bar absence is the most material: the mockup signals global search on every page; the live header has only "Sign out".

**What the mockup shows:**

- Breadcrumb chip: `Sentinel Labs > Controls` (tenant context in the chrome).
- Search input with placeholder `Search controls, evidence, risks…` and `⌘K` kbd hint, right-aligned in the top bar.
- Audit-in-progress pill: amber background, pulsing dot, text `SOC 2 Type II · Q2 2026 in progress`.
- User avatar circle (initials) + display name.

**What the live page shows:**

- Logo + `security-atlas` wordmark + `v0 · self-host` chip.
- `Sign out` button (right-aligned).
- TenantSwitcher component (`$L16`) renders, but it is a switcher, not a breadcrumb chip — and the mockup's chip presentation is qualitatively different.

**Evidence:**

- `Plans/mockups/controls.html` lines 23-54 (`TOP BAR` block).
- Live HTML at `/controls`: `<header class="flex h-14 shrink-0 items-center justify-between border-b bg-background px-6">` block — no `<input type="search">`, no `Sentinel Labs` text, no SOC 2 banner pill.

### Finding 2 — Scope filter pill missing from controls list (#224)

**Category:** (i) layout / chrome parity (Scope concept is real per canvas §5; the affordance is what's absent)
**Severity:** medium — Scope is a load-bearing constitutional primitive (invariant #4: "Scope is multidimensional"). Filtering controls by scope cell is the natural way to ask "what's the SOC 2 posture in prod-aws?" — the absence of the filter forces the user to read the full anchor list and intersect mentally.

**What the mockup shows:** `Scope` pill with options `All cells`, `env=prod`, `cloud=aws`, `bu=platform` (lines 172-180).

**What the live page shows:** Four pills only — Framework, Family, State, Freshness. `ControlFilters` in `web/app/(authed)/controls/filters.ts` has no `scope` key; the page's `FILTER_KEYS` array enumerates four keys.

**Evidence:**

- `Plans/mockups/controls.html` lines 172-180.
- `web/app/(authed)/controls/page.tsx` lines 86-91 (`FILTER_KEYS` array).
- Live HTML: `[data-testid="list-filter-pills"]` container holds four `<label data-testid="list-filter-pill-*">` elements.

### Finding 3 — "New control" button is silently disabled (#225)

**Category:** (ii) broken interaction / silent affordance
**Severity:** medium — slice 178's honesty heuristic explicitly flags `disabled` buttons whose tooltip reads "coming soon" or which have no explanation as HONESTY-GAPs. The current rendering shows the button at full visual weight with `disabled=""` and no tooltip / banner / explanation.

**What the mockup shows:** Primary-styled enabled "New control" button (line 122-124).

**What the live page shows:** `<button type="button" data-disabled="" tabindex="0" disabled="" ...>New control</button>` — primary-styled (`bg-primary text-primary-foreground`), disabled at the DOM level, with no `title=`, no adjacent banner, no "coming in slice #N" hint.

A user landing on `/controls` sees the button at full visual weight and reasonably expects it to work. Click → nothing. Same pattern slice 183 / 184 / 185 flagged for other pages.

**Evidence:**

- `Plans/mockups/controls.html` lines 121-124.
- `web/app/(authed)/controls/page.tsx` line 335 (`<Button size="sm" disabled>New control</Button>`).

### Finding 4 — Frameworks column missing from per-anchor rows (#226)

**Category:** (i) layout / chrome parity
**Severity:** low — informational column. The mockup's far-right column shows the framework set each anchor satisfies (e.g. `SOC2 · ISO · CSF` for `SCF:IAC-06`; `SOC2 · ISO · CSF · GDPR` for `SCF:CRY-04`). The data exists upstream (the SCF importer's STRM edges produce this set), but the controls list does not display it.

**What the mockup shows:** Seven columns — including a right-aligned `Frameworks` column listing satisfactions.

**What the live page shows:** Six columns — Frameworks column is omitted entirely. The `columns: ListColumn<AnchorRow>[]` array in `page.tsx` (lines 225-294) has six entries; none of them render framework satisfactions.

**Evidence:**

- `Plans/mockups/controls.html` line 197 (column header `<th class="text-right px-5 py-2.5 font-medium">Frameworks</th>`).
- `web/app/(authed)/controls/page.tsx` lines 225-294 (column array).
- BFF response shape: `AnchorWithState` does NOT carry a `frameworks` field — so this is a backend + frontend gap, not a pure presentational omission. A spillover that ships the column needs to extend the upstream `GET /v1/anchors?include=state` response (or add a second join key).

### Finding 5 — Pagination control absent (#227)

**Category:** (i) layout / chrome parity
**Severity:** low — at 53 anchors today the absence is tolerable. But the SCF catalog has ~1,400 anchors total; once a tenant's framework-scope predicate expands beyond the bootstrap seed, the unpaginated table becomes a usability issue. The mockup ships pagination at the bottom of the table; the live page renders all rows in a single page with no Prev/Next UI.

**What the mockup shows:** Footer row "Showing 1–7 of 82" + Previous (disabled) + Next buttons (lines 266-272).

**What the live page shows:** No pagination UI; `<ListTable>` renders every row from `visible` (the full filtered set). The page has no `?page=` query param handling, no `LIMIT` / `OFFSET` plumbing to upstream.

**Evidence:**

- `Plans/mockups/controls.html` lines 266-272.
- `web/app/(authed)/controls/page.tsx` — no pagination state, no `useState`/`useMemo` for page bounds, `<ListTable>` consumes `visible` directly.
- `web/components/list/list-table.tsx` (the shared list shell) — no pagination prop.

## Screenshots / DOM dumps

No screenshots committed (per slice 204 AC-7 / P0-A5 anti-criterion — admin-JWT HTML is scrubbed; no `Bearer` substring, no `atlas_jwt=` cookie value, no real-tenant content in this file).

## Notes for the maintainer

- Findings 1 + 4 + 5 share a theme: the mockup is more information-dense than the live page (top-bar wayfinding + per-row framework satisfactions + pagination chrome). If the maintainer wants to defer all three indefinitely, the _mockup_ needs updating instead (MOCKUP-STALE class per slice 178 vocabulary). But Scope (finding 2) and the disabled "New control" button (finding 3) are not in that bucket — they are real live-page gaps regardless of mockup direction.
- Finding 4 has a backend dependency: the BFF response does not include framework satisfactions today. The spillover slice notes this as the largest chunk of work in the set.
- The atlas-edge instance has 53 anchors (the bootstrap-seed SOC 2 SCF subset) — not 50 as the assignment brief suggested. Counting is honest in the live UI ("Showing 53 of 53").
