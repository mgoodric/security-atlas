# 204 — Page audit: /controls/{id} (detail view)

**Audit run:** 2026-05-23
**Live URL:** `https://atlas-edge.home.gmoney.sh/controls/1129867b-72fb-577f-996d-6f2f4bab2a68`
**HTTP status:** `200 OK` (HTML shell + SSR-rendered skeleton; client hydrates with `useQuery` + 404 → empty-state branch on this id)
**Mockup:** `Plans/mockups/control.html` (mockup title: "MFA Enforcement · security-atlas")
**Page slug:** `control`
**Page implementation:** `web/app/(authed)/controls/[id]/page.tsx` (slice 041)
**Upstream API surface:**

- `GET /v1/controls/{id}/coverage` (404 for the audit id — it is a global SCF anchor id, not a tenant control id)
- `GET /v1/controls/{id}/state` (404 — same reason)
- `GET /v1/controls/{id}/effectiveness` (404 — same reason)
- `GET /v1/controls/{id}/effective-scope?framework_version_id=…` (per framework)
- `GET /v1/controls/{id}/history`, `/policies`, `/risks` (exist on main per `internal/api/openapi/routes.go` lines 85-87 — **page does not consume them**)
- `GET /v1/evidence?control_id=…` (exists on main per slice 106; `internal/api/openapi/routes.go` line 93 — **page does not consume it**)

**Auth:** admin JWT (atlas_jwt cookie). Read-only audit; no clicks on mutating affordances.

## Parent slice

#204 — Comprehensive page-by-page UI parity audit (per-page agent fleet). Each finding below files as its own spillover slice per AC-2 / P0-A7.

## Method note — id used for the audit

The audit id `1129867b-72fb-577f-996d-6f2f4bab2a68` is a global SCF anchor (`AAA-01`, family AAA), not a tenant-instantiated control. On atlas-edge there are **zero** tenant controls (`GET /v1/controls?limit=3` returns `{"controls":[],"count":0}`), so a "happy path" with a real control is not reachable in this environment. The audit therefore evaluates two surfaces:

1. **The empty-state branch** (slice 152 / ADR-0004) that the live page renders for an unknown control id. Layout / chrome parity with the mockup is judged against this empty state.
2. **The render code path** (`web/app/(authed)/controls/[id]/page.tsx` lines 207-501) that the page executes when coverage resolves — judged statically against the mockup since no live tenant control reaches it. The slice 041 implementation notes are the ground truth for category (iv) staleness claims.

This is consistent with the prompt: "the parity check is on STRUCTURE (sections present, layout, interactions), not on the specific control's content."

## What the live page does today (factual baseline)

**For the audited id (SCF-anchor 404 path):**

- Renders the slice 152 / ADR-0004 empty-state card: SVG icon, "This SCF anchor has no control instantiated in your tenant yet" headline, explanatory paragraph naming the audited id, "Back to controls list" CTA.
- The top bar shows the standard authed shell (logo + sidebar + `Sign out`); no breadcrumb, no SOC 2 audit-in-progress banner, no user avatar, no search.

**For the happy path (static read of `web/app/(authed)/controls/[id]/page.tsx`):**

- Breadcrumb is a single `← All controls` link above the header (line 210-217). No tenant breadcrumb chain.
- Control header: `bundle_id · v{version}`, SCF-anchor pill (→ `/catalog/scf/{anchor.id}`), lifecycle badge, control_family, title h1, owner_role / implementation_type / freshness_class meta row.
- KPI strip: 4 cards — Effectiveness · 30d (`pass_rate.toFixed(2)` + pass/total), Frameworks satisfied (mapped fv count + "via SCF anchor"), Evidence records · 30d (**hardcoded "—" with sub-text "evidence-list endpoint pending"**), In-scope frameworks (`inScopeFrameworks` of mapped fv count).
- Coverage-by-framework table (CoverageTable component): columns _Framework requirement · STRM · Strength · Strength bar_ (four columns). **No Coverage column, no row-level chevron**.
- UCF mini graph (`UcfMiniViz`): renders.
- Evidence stream section: renders an `<Alert>` titled "Evidence stream not yet wired" that names `GET /v1/evidence?control_id=…` as "does not exist on main yet".
- Right rail: Freshness (component renders if `state` resolves), Effective scope (per-framework rows + cells count), Policies (empty-state text "endpoint not on main yet"), Risks treated (same), Audit log (same).
- No tab strip; the mockup's seven-tab strip (Overview / Evidence / Mappings / Effective scope / Policies / Risks / History) is absent.
- No header action buttons (mockup has Run query / Edit YAML / Request exception + "last evaluated 8m ago" timestamp).

## What the mockup promises (factual baseline)

- **Top bar** (lines 17-44): tenant breadcrumb (`Sentinel Labs > Controls > MFA Enforcement`), SOC 2 Type II audit-in-progress amber pill with pulsing dot, user avatar circle.
- **Control header** (lines 46-103): `CTRL-0014` id chip + SCF anchor pill + lifecycle pill + family tag · h1 + description paragraph · owner/implementation/freshness meta row · right-aligned action buttons "Run query", "Edit YAML", "Request exception" + "last evaluated 8 minutes ago" timestamp.
- **KPI strip** (lines 106-135): 4 cards — Effectiveness · 30d (with delta arrow), Evidence records · 30d (847, "28/day avg" sub), Frameworks satisfied (6), Effective scope cells (12 of 14).
- **Tab strip** (lines 139-152): Overview · Evidence (count) · Mappings (count) · Effective scope (count) · Policies (count) · Risks (count) · History — seven tabs, sticky under top bar.
- **Coverage-by-framework table** (lines 161-257): six columns — _Framework requirement · STRM · Strength · Coverage · Strength bar · chevron_. "Open graph view →" button in header. Footer text explaining "coverage is strength × 30-day effectiveness, intersected with the framework's scope predicate". Out-of-scope rows render with "out of FrameworkScope (no CDE)" inline annotation in the muted requirement-code sub-line.
- **UCF graph mini view** (lines 259-371): SVG graph + "Open in mappings inspector →" button.
- **Evidence stream** (lines 373-445): filter row (All / pass / fail / push / pull), 5 sample rows with timestamp · summary · connector tag · pass/fail badge · chevron, footer "View all 847 records →".
- **Right rail** (lines 448-558):
  - Freshness (clock SVG + "8m" + "since latest evidence" + "window: 7d · freshness class daily" + table rows _Oldest record in window_ / _Records past valid_until_).
  - Effective scope (per-framework progress rows with `12/14 cells` and progress bar + footer "applied in N scope cells; out-of-scope cells K (sandbox · public-data)").
  - Policies (rows: policy title + version line + status badge + "+ link" button in header).
  - Risks treated (RISK row: title + residual score + method/treatment line + "+ link" button).
  - Audit log (dated bullet entries: "Sam R. approved annual review", "Dana K. updated implementation_type to `automated`", etc.).

## Findings (one slice per finding)

| #   | Category                                          | Severity | Spillover | Subject                                                                                                            |
| --- | ------------------------------------------------- | -------- | --------- | ------------------------------------------------------------------------------------------------------------------ |
| 1   | (iv) Mockup-stale dead-text                       | high     | #253      | Four right-rail sections + evidence-stream + evidence-KPI claim "endpoint not on main yet" — the endpoints ship    |
| 2   | (i) Layout / chrome parity                        | medium   | #254      | Tab strip absent (seven tabs in mockup: Overview / Evidence / Mappings / Effective scope / Policies / Risks / Hx)  |
| 3   | (i) Layout / chrome parity                        | medium   | #255      | Header action buttons + "last evaluated" timestamp absent (Run query · Edit YAML · Request exception)              |
| 4   | (i) Layout / chrome parity + (iii) data-bound gap | medium   | #256      | Coverage column missing from coverage table; mockup shows strength × 30d-effectiveness weighted by frameworkscope  |
| 5   | (i) Layout / chrome parity                        | low      | #257      | Top bar omits tenant breadcrumb, audit-in-progress pill, user avatar (shared chrome shortfall across detail pages) |

**No fabricated-data category (iii) lies on the empty-state path.** The empty-state branch is honest: it names the audited id, explains why no tenant control resolves it, and surfaces a Back-to-list CTA. The coverage-table omission in Finding 4 has a category-(iii) shade — the missing Coverage column is a data-bound surface that the backend can compute but the page elects not to render — so the finding is dual-tagged.

## Detailed findings

### Finding 1 — Five mockup-stale "endpoint not on main yet" empty-states for endpoints that ship (#253)

**Category:** (iv) mockup-stale (the page comment describes endpoints as missing that have shipped)
**Severity:** high — five user-facing surfaces (the right-rail Policies / Risks / Audit log cards, the evidence-stream center-column card, and the evidence-records KPI cell) render text claiming the backing endpoint "is not on main yet". Five claims, four false (one — the per-control evidence list — was true at slice 041 but has been true on main since slice 106).

**What the live page renders:**

- KPI strip (line 302-306): `<KpiCard label="Evidence records · 30d" value="—" sub="evidence-list endpoint pending" />`
- Evidence stream section (lines 359-384): `<Alert>` reading "The evidence stream binds to a `GET /v1/evidence?control_id=…` list endpoint that does not exist on main yet…"
- Right-rail Policies card (lines 458-469): "Linked policies bind to a per-control policy-link read endpoint that is not on main yet."
- Right-rail Risks card (lines 471-483): "Linked risks bind to a per-control risk-link read endpoint that is not on main yet."
- Right-rail Audit log card (lines 485-497): "The per-control audit log binds to a control-history read endpoint that is not on main yet."

**What main actually ships** (`internal/api/openapi/routes.go`):

- Line 93: `GET /v1/evidence` (slice 106 — supports `?control_id=…`; confirmed live: `GET /v1/evidence?control_id=<uuid>` → 200 with `{count, evidence[], next_cursor}`)
- Line 85: `GET /v1/controls/{id}/history` (confirmed live: 200 with `{control_id, count, history[], next_cursor}`)
- Line 86: `GET /v1/controls/{id}/policies` (confirmed live)
- Line 87: `GET /v1/controls/{id}/risks` (confirmed live: 200 with `{control_id, count, risks[]}`)

**Why this is high-severity:** the UI-honesty constitutional anti-pattern reads in both directions. Promising what we don't have is the canonical violation; **denying what we do have is the dual** — the operator looking at this page concludes the platform is less complete than it is, and downstream slices keep filing "wire this surface" issues that have already shipped backends. Slice 041's comment block was correct at the time; the page has not been re-walked since the slice-106 / per-control history-policies-risks endpoints landed.

### Finding 2 — Tab strip absent (#254)

**Category:** (i) layout / chrome parity
**Severity:** medium — the mockup's tab strip (lines 139-152) is the page's primary navigation affordance for the detail view. Seven tabs (`Overview`, `Evidence`, `Mappings`, `Effective scope`, `Policies`, `Risks`, `History`) each with a count chip. Live page has none — the entire detail view is a single scroll on one tab equivalent ("Overview").

**Why this matters:** the mockup's information density assumes a tabbed view (e.g., Evidence and Mappings are full-page interactions, not in-page sections). Collapsing them to one scrolling page is a real design departure, not a styling delta. The detail page renders ~7 sections vertically already; with a real tenant control populating the data, the page would be very tall and the right rail would float far off-screen for any user reading the bottom of the coverage table.

**Notable nuance:** the live page does ship `tabs` UI primitives (used in `/audits/[id]/workspace` per slice 044 / control-workspace component). So this is a design omission, not a missing component library.

### Finding 3 — Header action buttons + "last evaluated" timestamp absent (#255)

**Category:** (i) layout / chrome parity
**Severity:** medium — three header buttons (Run query, Edit YAML, Request exception) plus a "last evaluated 8 minutes ago" sub-line are absent from the live header. The buttons are top-of-funnel action affordances; their absence on the most important per-control page is a discoverability problem.

**Caveat:** the "Run query" button implies a control-as-code execution affordance (canvas §4.5 — rule-DSL / control evaluation surface) that is plausibly v2+. "Edit YAML" implies a control-text editor — same scope. "Request exception" implies the exception-request workflow (canvas §4.6 / slice family in the 110s). All three are real concepts in the canvas. The spillover should land the buttons as labelled affordances with "not yet wired" sub-text, or — preferably — as a no-op surface that explains the design intent (avoiding the Finding-1 trap of claiming endpoints don't exist).

**The "last evaluated" timestamp,** by contrast, is trivially wireable today — the freshness-clock component in the right rail already renders `state.last_observed_at`. Mirroring it next to the header buttons is a copy-paste.

### Finding 4 — Coverage column missing from coverage table; mockup shows weighted coverage (#256)

**Category:** (i) layout / chrome parity + (iii) data-bound surface omission
**Severity:** medium — the mockup's coverage table has six columns; the live table has four. The missing two are _Coverage_ (strength × 30d-effectiveness, intersected with framework scope) and a row-level chevron. Coverage is the page's headline metric — the mockup's footer even calls it out: "coverage is strength × 30-day effectiveness, intersected with the framework's scope predicate". Without the column the table is just a re-render of the mappings list, not a coverage view.

**The slice 041 implementation comment** (`coverage-table.tsx` lines 11-17) explicitly opts out: "The slice does NOT recompute strength × effectiveness per row — that weighted number is a framework-dashboard concern…and fabricating it here would risk a number that disagrees with the backend." This is a sound argument _at the time slice 041 shipped_, but the consequence is that the page's most prominent table omits its load-bearing column. The fix shape is either:

- (a) compute the weighted coverage on the backend (`GET /v1/controls/{id}/coverage` adds a `coverage` field per requirement that reads `strength * effectiveness * framework_scope_predicate_satisfied`), then the page renders it. This is the clean answer.
- (b) compute it client-side from the already-fetched `effectiveness` payload and `outOfScopeFvIds` set. Cheaper, but exactly the "risk of disagreement with the backend" the slice 041 comment warns about.

The spillover should land (a) — it's a one-field backend addition and a one-column frontend addition.

**The chevron column** is a smaller omission — mockup uses it to signal each row is clickable (drills into a per-requirement mappings view). Live page has no per-row drill. Bundling the chevron + drill destination with the coverage column makes the row navigable too.

### Finding 5 — Top bar omits tenant breadcrumb, audit-in-progress pill, user avatar (#257)

**Category:** (i) layout / chrome parity
**Severity:** low — this is the same shared-chrome shortfall flagged by spillover #223 (controls list audit). Filing it as a separate finding here because the audit fleet's per-page rule is one slice per finding per page, but the implementation fix is presumably a single shared-header slice that closes both #223 and #257 (and likely the equivalent on the other detail pages — risks, audits, policies). Cross-reference #223 in the slice body so the maintainer can close them together.

**What's missing on the detail page specifically:**

- Tenant breadcrumb chain (mockup: `Sentinel Labs > Controls > MFA Enforcement`). Live page has `← All controls` only — no tenant context, no third-level "this control's name" leaf.
- SOC 2 Type II audit-in-progress amber pill (mockup lines 35-38). Live: absent.
- User avatar circle (mockup line 40). Live: only "Sign out" button (TenantSwitcher component renders but is not the avatar).

## Audit hygiene

- No `Bearer`, `atlas_jwt=`, `atlas_session=`, or `eyJ*` tokens in any committed file (AC-7 scrub clean).
- No real-tenant screenshots or content in any report (AC-8).
- Read-only audit; no clicks on mutating affordances.
- All spillover slices cite #204 as parent per AC-6.
- Spillover number range respected: 253-257 (max 5; not exceeded).
- No production code touched (anti-criterion: NO inline fixes).
- No `_STATUS.md` or `CHANGELOG.md` updates (anti-criterion).
- No slice 204 spec modification (anti-criterion).
