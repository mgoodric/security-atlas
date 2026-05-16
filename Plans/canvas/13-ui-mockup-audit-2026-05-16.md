# 13 — UI vs mockup audit (2026-05-16, post-slice-093)

> One-pass audit comparing the current Next.js implementation against the
> mockups under `Plans/mockups/` after slice 093 (mockups for 6 missing
> top-level pages) merged. Captures drift that needed in-place repair plus
> per-page gaps that follow-on implementation slices (098–103) cover.

---

## Scope

Compared:

1. **App shell** — `web/components/shell/sidebar.tsx` against design-decisions doc §1 (canonical top-nav order from slice 093)
2. **Existing implemented pages** that have a mockup counterpart — sampled `/dashboard` against `Plans/mockups/dashboard.html`
3. **Routes vs. nav** — every sidebar entry that resolves to an actual page vs. those that 404 today

Out of scope:

- Per-page visual-detail drift on `/board-packs`, `/controls/[id]`, `/dashboards/metrics`, `/calendar`, `/risks/hierarchy`, `/catalog/scf` — those audits ride on the next slice that touches each page (audit-at-edit-time, not audit-at-rest).
- Routes with no mockup precedent (e.g. `/vendors`, `/framework-scopes/[id]`) — they shipped before the mockup-first convention. Re-audit when a mockup is later drafted.

---

## Findings

### F-1 · Sidebar order does not match design-decisions doc §1 (HIGH)

**Observed:** `web/components/shell/sidebar.tsx:5-22` orders the nav as:

> Dashboard · Calendar · Metrics · Catalog · SCF · Controls · Evidence · Risks · Risk hierarchy · Policies · Vendors · Audits · Settings · Admin

**Expected per `Plans/canvas/12-ui-fill-in-design-decisions.md` §1:**

> Dashboard · Controls · Evidence · Risks · Audits · Policies · Vendors · Board Packs · Settings · Admin

**Drift:**

- **Audits at position 11** (after Vendors) but should be **position 5** (in the core-5 cluster after Risks). The design-doc rationale: "Audits is opened in bursts (period setup, sample review, freeze)" — keeping it in the core-5 makes it discoverable when an audit period kicks off.
- **Board Packs missing** entirely. The `/board-packs` route exists and has a mockup (`Plans/mockups/board-pack.html`); the sidebar omits it.

**Fix in this slice:** reorder + add Board Packs. Calendar / Metrics / Catalog · SCF are legitimate post-93 additions and stay (see F-2 for the design-doc evolution).

### F-2 · Design doc §1 doesn't reflect Calendar / Metrics / Catalog · SCF (MEDIUM)

**Observed:** slices 094 (calendar) and 097 (metrics) shipped sidebar entries that aren't in the canonical §1 list. Catalog · SCF predates slice 093 entirely.

**Why this is drift, not violation:** the design-decisions doc was authored during slice 093 (which only saw the 6 follow-up pages it was unblocking). Calendar + Metrics filed later. Catalog · SCF is reference content, not per-tenant data, and was reasonable to omit from a "core list-views" exercise.

**Fix in this slice:** extend §1 with three new entries + rationale for their placement.

### F-3 · Risk hierarchy exposed at top-nav, contradicting design doc §5 (LOW · deferred to slice 101)

**Observed:** the sidebar has both `/risks` AND `/risks/hierarchy` as top-level entries.

**Design doc §5 says:** "/risks shows a `Hierarchy view →` link in the page header. /risks/hierarchy should add a reciprocal `List view →` link when its slice gets a refresh."

**Why deferred:** removing Risk hierarchy from top-nav today (before `/risks` exists) leaves the user with no path to the hierarchy view. Slice 101 (`/risks` list view) is the right place to land this — it ships the page-header link AND removes the redundant sidebar entry in the same PR.

### F-4 · 6 sidebar entries 404 today (HIGH · resolved by slices 098–103)

**Observed:** sidebar links to `/controls`, `/evidence`, `/risks`, `/policies`, `/audits`, `/settings` — none of which have a `page.tsx` under `web/app/(authed)/<route>/`. Clicking them today returns a Next.js 404.

**Fix:** slices 098–103 each ship one of the six missing pages. Forward-declaration of the links is acceptable because (a) the canonical nav is the user-facing contract, (b) the loop is actively unblocked to land the six, (c) a transient 404 on a freshly-deployed shell is less bad than re-shuffling the nav twice.

### F-5 · `/dashboard` implementation matches mockup conceptually (PASS)

**Sampled:** `web/app/(authed)/dashboard/page.tsx:28-33` imports six panels (ActivityFeed, EvidenceFreshness, FrameworkPosture, RecentDrift, TopRisks, Upcoming) that mirror the six-panel layout in `Plans/mockups/dashboard.html`. The implementation comment at lines 4-22 explicitly cites the mockup as the design reference and acknowledges the two missing-endpoint panels as documented placeholders.

**No drift action required.** A visual-pixel audit (color drift, spacing) is out of scope for this pass — defer to a focused design-pass slice if/when the maintainer wants pixel-level fidelity.

---

## Counts

| Finding | Severity | Fix location                                        |
| ------- | -------- | --------------------------------------------------- |
| F-1     | HIGH     | This slice — sidebar.tsx reorder + Board Packs add  |
| F-2     | MEDIUM   | This slice — design doc §1 extension                |
| F-3     | LOW      | Deferred to slice 101 (`/risks` list view)         |
| F-4     | HIGH     | Slices 098–103 (one per missing page)              |
| F-5     | PASS     | None — no drift                                     |

**Net actions this PR:** 2 in-place fixes (F-1, F-2) + 6 new ready slices filed (F-4).
