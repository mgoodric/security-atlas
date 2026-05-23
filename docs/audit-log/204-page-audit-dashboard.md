# 204 — Per-page UI parity audit: `/dashboard`

**Parent:** slice 204 (UI parity audit fleet)
**Audited at:** 2026-05-23 (live build: `https://atlas-edge.home.gmoney.sh`)
**Mockup file:** `Plans/mockups/dashboard.html`
**Live URL:** `https://atlas-edge.home.gmoney.sh/dashboard`
**Page slug:** `dashboard`
**Audit agent:** background subagent (fleet member), read-only, no production code touched
**Slice number range used:** 228–232 (5 of 5 reserved)

## Method

1. Read the mockup HTML (`Plans/mockups/dashboard.html`, 619 lines) — enumerated visible panels, chrome elements, and CTAs.
2. Fetched the server-rendered live HTML via `curl --cookie atlas_jwt=...` (HTTP 200, 19-line minified Next.js output) and inspected for structural elements + skeleton-loading testids.
3. Probed each panel's backend endpoint with the admin JWT:
   - `GET /api/dashboard/framework-posture` → HTTP 200, `{count:0, frameworks:[]}`
   - `GET /api/dashboard/risks` → HTTP 200, `{risks:[], count:0}`
   - `GET /api/dashboard/freshness` → HTTP 200, `{bucket:"class", buckets:[], total:0, total_stale:0}`
   - `GET /api/dashboard/drift` → HTTP 200, `{delta:0, flipped_out:[], flipped_out_count:0, since:"2026-05-16", through:"2026-05-23"}`
   - `GET /api/dashboard/upcoming` → HTTP 200, `{count:0, next_cursor:"", upcoming:[]}`
   - `GET /api/dashboard/activity` → HTTP 200, `{activity:[], count:0, next_cursor:""}`
4. Read each panel component (`web/components/dashboard/*.tsx`) for empty-state handling, dead anchors, and "coming soon" placeholders (slice 178 HONESTY-GAP class).
5. Cross-checked the live page's testids (`framework-posture-panel`, `top-risks-panel`, `evidence-freshness-panel`, `recent-drift-panel`, `upcoming-panel`, `activity-feed-panel`) against the mockup's panel inventory.

## Headline finding

**The v1.14.0 500-error class noted in the slice 204 spec is RESOLVED for `/dashboard`.** All six panel endpoints return clean HTTP 200 with empty payloads. The dashboard renders six honest empty-state panels — no 500s, no infinite skeleton spinners, no auth-redirect loops. The post-206 / 208 / 209 / 210 / 211 fixes are working as intended on this surface.

**The remaining findings are pure UI parity gaps** — places where the mockup encodes a design intent that the live build either has not shipped (ship-gap) or has shipped differently (parity-divergence). None are HONESTY-GAPs in the slice 178 sense (the live build does NOT lie about what it has — empty panels render with explicit empty-state copy, disabled filter chips render with explanatory tooltips, no dead "coming soon" buttons).

## Findings

| #   | ID       | Category                      | Severity guess | Subject                                                                                                               | Spillover                                                                                 |
| --- | -------- | ----------------------------- | -------------- | --------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| 1   | F-204D-1 | (i) layout / chrome           | medium         | Global `⌘K` search bar missing from topbar (mockup lines 43–47)                                                       | [#228](../issues/228-ui-honesty-dashboard-global-search-bar-missing.md)                   |
| 2   | F-204D-2 | (i) chrome + (iii) data-bound | medium         | Dashboard header lacks tenant + snapshot-freshness subtitle context                                                   | [#229](../issues/229-ui-honesty-dashboard-header-tenant-snapshot-context-missing.md)      |
| 3   | F-204D-3 | (i) layout / chrome           | medium         | Header right-side "Export" + "New board report" action buttons missing                                                | [#230](../issues/230-ui-honesty-dashboard-export-and-new-board-report-actions-missing.md) |
| 4   | F-204D-4 | (iv) mockup-stale             | low            | Topbar persistent "SOC 2 Type II · Q2 2026 in progress" status pill — no backing code path; recommend mockup deletion | [#231](../issues/231-ui-honesty-dashboard-mockup-stale-audit-cycle-status-pill.md)        |
| 5   | F-204D-5 | (i) chrome (ship-gap)         | low            | Activity-feed panel lacks "View full activity ledger →" footer link                                                   | [#232](../issues/232-ui-honesty-dashboard-activity-feed-ledger-footer-link-missing.md)    |

### F-204D-1 — Global `⌘K` search bar missing from topbar

**Mockup evidence:** `Plans/mockups/dashboard.html` lines 43–47 render a 256px-wide search input pinned right in the topbar with a `⌘K` keyboard-shortcut badge and placeholder `"Search controls, evidence, risks…"`.

**Live evidence:** `web/components/shell/topbar.tsx` line 36 renders an `h-14` topbar containing only the logo (left) + `TenantSwitcher` + "Sign out" button (right). No search input. Codebase grep for `⌘K`, `cmd+k`, or a global-search input returns no matches in the shell components.

**Category:** (i) layout / chrome parity.
**Severity guess:** medium — primary nav affordance for the solo-security-leader persona.
**Spillover:** [#228](../issues/228-ui-honesty-dashboard-global-search-bar-missing.md), filed `not-ready` (depends on a backing unified-search endpoint or a UI-side fan-out design).

### F-204D-2 — Dashboard header lacks tenant + snapshot-freshness subtitle

**Mockup evidence:** `Plans/mockups/dashboard.html` lines 117–124 render H1 `"Program"` followed by tenant context `"Sentinel Labs · production"` and subtitle `"Snapshot taken 18 minutes ago · evidence freshness 87% within window"`.

**Live evidence:** `web/app/(authed)/dashboard/page.tsx` lines 96–102 render H1 `"Program"` followed only by generic marketing subtitle `"The home screen for the security program — live posture, drift, risk, and what is coming up."`. The freshness query data and the active-tenant context are already available — the chrome simply doesn't bind to them.

**Category:** (i) chrome + (iii) data-bound surface that doesn't surface what it could.
**Severity guess:** medium — operator orientation in 1 line.
**Spillover:** [#229](../issues/229-ui-honesty-dashboard-header-tenant-snapshot-context-missing.md), filed `ready`.

### F-204D-3 — "Export" and "New board report" header CTAs missing

**Mockup evidence:** `Plans/mockups/dashboard.html` lines 125–131 render two header action buttons: secondary "Export" + primary "New board report".

**Live evidence:** `web/app/(authed)/dashboard/page.tsx` lines 94–103 render no action buttons in the H1 row.

**Category:** (i) layout / chrome parity.
**Severity guess:** medium — primary "act on what you see" affordances are absent.
**Spillover:** [#230](../issues/230-ui-honesty-dashboard-export-and-new-board-report-actions-missing.md), filed `not-ready` ("Export" lacks a backing endpoint; "New board report" is implementable today but the implementing slice may split).

### F-204D-4 — Topbar persistent audit-cycle status pill (mockup-stale)

**Mockup evidence:** `Plans/mockups/dashboard.html` lines 38–42 render an amber pill `"SOC 2 Type II · Q2 2026 in progress"` with a pulsing dot, left of the search bar.

**Live evidence:** No matching element. Codebase grep for the literal string and conceptual variants (`audit-cycle`, persistent topbar audit-period rendering) returns zero matches in shell components. Audit-period state surfaces inside `/audits` (slice 042's audit-period card), not in global chrome.

**Category:** (iv) mockup-stale. Recommendation: remove the pill from the mockup (slice 183 precedent for mockup-vs-production divergence resolution).
**Severity guess:** low — mockup hygiene; no live-build defect.
**Spillover:** [#231](../issues/231-ui-honesty-dashboard-mockup-stale-audit-cycle-status-pill.md), filed `ready`.
**Resolution:** Slice 231 removed the amber pill `<div>` block at `Plans/mockups/dashboard.html` lines 38–42 and replaced it with an in-place HTML comment recording the MOCKUP-STALE deletion and the rationale (audit-period state belongs in the `/audits` workspace, not global chrome). Mockup-only edit; no production code changed. Decisions log: [`docs/audit-log/231-decisions.md`](231-decisions.md).

### F-204D-5 — Activity-feed panel lacks "View full activity ledger →" footer link

**Mockup evidence:** `Plans/mockups/dashboard.html` lines 608–610 render a centered footer link below the activity events.

**Live evidence:** `web/components/dashboard/activity-feed-panel.tsx` renders no footer link. The full ledger destination (slice 067's `/admin/audit-log`) exists but is admin-gated — straightforward "add the link" runs into slice 186's affordance-honesty constraint for non-admin users.

**Category:** (i) chrome — ship-gap, not mockup-stale (the destination concept is real).
**Severity guess:** low — secondary affordance.
**Spillover:** [#232](../issues/232-ui-honesty-dashboard-activity-feed-ledger-footer-link-missing.md), filed `not-ready` (depends on either a non-admin ledger route or the slice 186 role-conditional pattern).

## Confirmed honesties (not findings)

These are surfaces where the audit specifically looked for HONESTY-GAPs and found honest behavior — recorded for the maintainer's confidence:

- **Empty-state copy.** Each panel renders an explicit empty-state string when its query returns zero records:
  - Framework posture: `"No active framework versions yet. Import a framework catalog to populate posture tiles."`
  - Top risks: `"No risks are currently in the mitigate treatment state."`
  - Activity feed: `"No evidence-ingest activity yet. Push evidence via a connector or the CLI to populate this feed."`
  - The other three panels render comparable strings. No panel shows fake placeholder data; no panel shows an infinite skeleton.
- **Activity-feed filter chips.** The `All / Evidence / Controls / Approvals` chips render with `aria-disabled="true"`, `cursor-not-allowed` styling, and a tooltip explaining the backend limitation (`title="Filter chips activate once the activity endpoint widens beyond the evidence branch"`). This is the slice 178 disabled-with-tooltip honesty pattern executed correctly (slice 147 wrote the comment that documents why).
- **Top-risks "View register →" link.** Real route (`/risks`), not a `href="#"` dead anchor.
- **No 500 errors.** All six panel endpoints return clean HTTP 200. The v1.14.0 500-error class noted in slice 204's spec is resolved for this surface (post-206 / 208 / 209 / 210 / 211 fix chain working).
- **Sidebar Vendors / Admin entries.** Live sidebar correctly renders `/vendors` (real route per slice 092) and `/admin` (role-gated per slice 186) — both are honest. The mockup's two stale entries (slice 183's removed Vendors + Admin) confirm the mockup, not the live, was the source of drift.

## Items NOT filed (intentional)

- **No HONESTY-GAP findings.** The live dashboard does not contain "coming soon" buttons, dead anchors, or visible-but-non-functional widgets. Slice 040's design intent (six honest panels, each rendering its own empty state on zero-data) is intact.
- **No 500-class diagnostic spillover.** Per slice 204 P0-A4 and AC-11, the agent does not debug 500s — and on this surface there are none to debug.
- **No re-file of slice 178's already-merged findings** (162/163/164/183/184/185/186). Verified against `docs/issues/` for overlap.

## Screenshots

None captured. The audit relied on server-rendered HTML inspection + endpoint probing + source-code reading. The page's authenticated content is structurally minimal (six skeleton-loading panels at server-render time; client-side React hydration fills them). No screenshot would add information beyond what the HTML dump + component source already provides. Per slice 204 mitigation I, this also keeps the audit log free of any potential tenant data — even from the bootstrap seed environment.

## Anti-criteria audit (slice 204 P0 checklist)

- **P0-A1 (no inline fixes):** confirmed. No production code touched.
- **P0-A2 (only docs/audit-log + docs/issues files modified):** confirmed.
- **P0-A3 (no schema / migration / fixture changes):** confirmed.
- **P0-A4 (no 500 debugging):** N/A — no 500s present.
- **P0-A5 (no Bearer / atlas_session / tenant screenshots committed):** confirmed. No screenshots. No JWT values, no session cookies, no tenant-identifying captures in this file. The JWT was used for endpoint probing only; only the (publicly-known) endpoint shapes and zero-count empty responses are recorded.
- **P0-A6 (≤4 concurrent agents):** N/A — this is one fleet member; orchestrator owns the cap.
- **P0-A7 (one finding per slice):** confirmed. Five findings, five spillover slices, no bundling.
- **P0-A8 (no vendor-prefixed test tokens):** N/A — this audit didn't write any fixtures.

## Summary for the aggregate report

- 5 findings filed (228–232)
- 0 HIGH severity; 3 MEDIUM (1/2/3); 2 LOW (4/5)
- 3 `not-ready` (depend on backing endpoints or design decisions): 228, 230, 232
- 2 `ready`: 229, 231
- 0 inline fixes attempted; 0 production code touched

The dashboard's panel-level behavior is solid post-fix-chain. The remaining gaps are chrome-level: missing top-bar search, missing header CTAs, and a missing subtitle-binding. These are independently shippable in 0.25d–0.5d each.
