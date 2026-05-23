# 204 page audit — `/policies`

**Audited page URL:** `https://atlas-edge.home.gmoney.sh/policies`
**Mockup HTML:** `Plans/mockups/policies.html`
**Audit branch:** `audit/204-policies`
**Audit date:** 2026-05-23
**Auditor:** slice 204 per-page agent fleet
**Backing slices for the live surface:** #022 (policy primitive +
list endpoint), #101 (list view), #107 (joined ack-rate cell),
#138 (CSV/JSON/XLSX export buttons)

## Audit method

1. Read mockup HTML at `Plans/mockups/policies.html` (336 lines).
2. Fetched live HTML via `curl --cookie atlas_jwt=<admin JWT>
https://atlas-edge.home.gmoney.sh/policies` (HTTP 200; 38578 bytes).
3. Probed `/v1/policies` (HTTP 200; `{"count":0,"policies":[]}`)
   and `/v1/policies?include=ack_rate` (same).
4. Probed adjacent endpoints surfaced by the mockup vocabulary:
   `/v1/policy-attestations` (404 — endpoint not exposed at edge),
   `/v1/policies:scaffold` (404 — endpoint does not exist).
5. Read the rendered HTML for filter-pill, action-button,
   pagination-footer, and empty-state markup. Read
   `web/app/(authed)/policies/page.tsx` to confirm runtime behavior
   for findings that depend on client-side rendering (the live
   HTML served includes a loading skeleton; the final state is
   client-hydrated empty-state for the zero-row tenant under audit).
6. Classified each finding into the four slice-204 categories.

## Compared on four axes

### (i) Layout / chrome parity

| Element                        | Mockup                                                                               | Live                                                                                                                                                           | Finding                                                                                       |
| ------------------------------ | ------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| Page title                     | `Policy library`                                                                     | `Policy library`                                                                                                                                               | match                                                                                         |
| Subtitle                       | "Versioned policies · acknowledgment tracked against current version only"           | "Versioned policies · acknowledgment tracked against the current version"                                                                                      | trivial copy drift — NOT filed (cosmetic)                                                     |
| Title-bar inline status counts | "14 published · 2 draft · 1 retired" next to `<h1>`                                  | not rendered                                                                                                                                                   | **F-204-policies-2** → slice 239                                                              |
| Action buttons (right rail)    | 2 active: `Acknowledgment report` + `New policy`                                     | 5 buttons: 3 active Export CSV/JSON/XLSX (slice 138 addition; legitimate post-mockup feature) + 2 disabled `Acknowledgment report` + `New policy` (lying CTAs) | **F-204-policies-4** → slice 241 (disabled CTAs); Export trio is legit-post-mockup, NOT filed |
| Filter pill row                | 4 pills: Status / Owner role / Linked control / Ack status                           | 2 pills: Status / Owner role                                                                                                                                   | **F-204-policies-1** → slice 238                                                              |
| Table columns                  | Title, Version, Status, Owner role, Published, Acknowledgment, Updated               | Same set                                                                                                                                                       | match                                                                                         |
| Pagination + window footer     | `Showing 1–7 of 17 · 365-day acknowledgment window` + `[Previous] [Next]`            | not rendered                                                                                                                                                   | **F-204-policies-3** → slice 240                                                              |
| Empty-state card               | renders with "No policies published yet" + "Scaffold five foundational policies" CTA | renders identically                                                                                                                                            | layout match — but see (ii) for CTA-destination lie                                           |
| Loading skeleton               | mockup shows 3-row skeleton                                                          | live renders `list-loading-skeleton-row` while query pending                                                                                                   | match                                                                                         |

### (ii) Broken interactions

| Affordance                               | Behavior in live                                                                                                          | Finding                                                                                                                                                                                                    |
| ---------------------------------------- | ------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Acknowledgment report` button           | rendered `disabled` without tooltip / explanation                                                                         | **F-204-policies-4** → slice 241                                                                                                                                                                           |
| `New policy` button                      | rendered `disabled` without tooltip / explanation                                                                         | **F-204-policies-4** → slice 241 (same root cause; bundled as one slice per slice 204 "one finding per slice" interpreted as one _class_ per slice; both buttons share the disabled-without-tooltip class) |
| Export CSV/JSON/XLSX buttons (slice 138) | render as active `<a>` to `/api/admin/policies/export?format=...` — link confirmed via HTML inspection                    | working — NOT filed                                                                                                                                                                                        |
| Empty-state CTA                          | `Scaffold five foundational policies` → `router.push("/admin/credentials")` (unrelated admin page; not a scaffold wizard) | **F-204-policies-5** → slice 242                                                                                                                                                                           |
| Filter pill `Status`                     | shape matches `FilterPills` slice 098 primitive; URL-driven filter state in place                                         | working — NOT filed                                                                                                                                                                                        |
| Filter pill `Owner role`                 | shape matches; URL-driven filter state in place                                                                           | working — NOT filed                                                                                                                                                                                        |
| Row click                                | navigates to `/policies/[id]` per `web/app/(authed)/policies/page.tsx` `onRowClick`                                       | live tenant has zero rows so cannot observe end-to-end; slice 183 already flagged the `/policies/<id>` detail-route 404 family — NOT re-filed here (de-duped against slice 183)                            |
| Search (header `⌘K`)                     | mockup-only chrome; the production global header has its own search shell                                                 | global chrome, NOT page-specific — NOT filed (covered by index/dashboard audit)                                                                                                                            |

### (iii) Data-bound surfaces

| Mockup claim                                                        | Live behavior                                                                                                      | Finding                                                                                                                                      |
| ------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------- |
| Mockup shows 7 sample policy rows w/ ack rates                      | Live tenant returns `{"count":0,"policies":[]}` — empty state renders                                              | working as designed for a fresh tenant; the audit cannot evaluate populated-state honesty without seed data; deferred to a seed-harness pass |
| Mockup shows acknowledgment column with `<percent> · <num>/<denom>` | `web/app/(authed)/policies/page.tsx` cell renders identical shape from `row.ack_rate` (joined cell from slice 107) | working — NOT filed                                                                                                                          |
| Mockup header counts (14/2/1)                                       | not rendered (see (i))                                                                                             | **F-204-policies-2** → slice 239                                                                                                             |

### (iv) Mockup-stale

| Mockup element                                                                                                         | Status                                                                                                            | Finding                                                                                                         |
| ---------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| `Linked control` filter pill (SCF:IAC-06 · MFA / SCF:CHG-04)                                                           | no policy↔control linkage surface exists on the wire; backing data missing                                       | bundled into **F-204-policies-1** → slice 238 (AC-4 defers Linked-control to a follow-on requiring wire change) |
| `Scaffold five foundational policies` CTA → wizard                                                                     | no wizard exists; `/v1/policies:scaffold` is 404; the CTA's onClick redirects to `/admin/credentials` placeholder | **F-204-policies-5** → slice 242                                                                                |
| `Acknowledgment report` button                                                                                         | no report-generation surface exists on the platform                                                               | **F-204-policies-4** → slice 241                                                                                |
| `New policy` button (wizard / form)                                                                                    | no policy-create UI exists; `POST /v1/policies` works but no UI surface invokes it                                | **F-204-policies-4** → slice 241 (same disabled-CTA class)                                                      |
| `365-day acknowledgment window` footer disclosure                                                                      | no UI surface discloses the window length anywhere on the page                                                    | bundled into **F-204-policies-3** → slice 240                                                                   |
| Mockup header chrome (`Sentinel Labs > Policies` breadcrumb, SOC 2 Type II amber pill, `⌘K` search, `MG / Sam` avatar) | These are mockup-only — the production shell has its own header chrome.                                           | global chrome inconsistency — NOT page-specific; covered by index/dashboard audits, NOT re-filed here           |

## Findings summary

| Finding ID       | Category                                    | Severity guess | Spillover slice | Brief description                                                                                                                                       |
| ---------------- | ------------------------------------------- | -------------- | --------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F-204-policies-1 | (iv) mockup-stale + (i) layout-parity       | medium         | **#238**        | Missing filter pills `Ack status` (backing data exists) + `Linked control` (deferred — no backing wire)                                                 |
| F-204-policies-2 | (i) layout-parity                           | low            | **#239**        | Inline `N published · M draft · K retired` count summary missing from the page title row                                                                |
| F-204-policies-3 | (i) layout-parity + (iv) mockup-stale       | medium         | **#240**        | Pagination footer + `365-day acknowledgment window` disclosure not rendered                                                                             |
| F-204-policies-4 | (ii) broken-interaction + (iv) mockup-stale | medium-high    | **#241**        | `Acknowledgment report` + `New policy` buttons render `disabled` without tooltip / link — lying-CTA honesty class                                       |
| F-204-policies-5 | (ii) broken-interaction + (iv) mockup-stale | high           | **#242**        | Empty-state `Scaffold five foundational policies` CTA redirects to unrelated `/admin/credentials` page — high-visibility lie on first-tenant onboarding |

5 findings, 5 spillovers (slice 238 through 242). No findings
escalated to the v1.14.0 500-error class (the page renders cleanly
against `atlas-edge.home.gmoney.sh`).

## Notes for the orchestrator

- The Export CSV/JSON/XLSX trio (slice 138) is legitimate
  post-mockup capability — NOT filed as mockup-stale (the mockup
  pre-dates the export feature).
- The `/policies/<id>` detail-route 404 family was already filed
  via slice 183 (calendar dead-link family) which covers
  `/policies/<id>` 404s. Not re-filed.
- The trivial subtitle copy drift ("…current version only" vs
  "…the current version") is cosmetic; intentionally not filed.
- The live tenant under audit (atlas-edge dev seed) returns zero
  policy rows, so populated-state honesty (does the live UI render
  the same 7-row shape the mockup shows?) cannot be evaluated
  end-to-end. Deferred to a seed-harness pass; not filed as a
  separate finding because the implementation IS structurally
  correct per code review of `web/app/(authed)/policies/page.tsx`.

## Pre-commit scrub

`grep -rE "Bearer [A-Za-z0-9._-]+|atlas_session=[A-Za-z0-9._-]+|atlas_jwt=[A-Za-z0-9._-]+" docs/audit-log/204-page-audit-policies.md docs/issues/238-*.md docs/issues/239-*.md docs/issues/240-*.md docs/issues/241-*.md docs/issues/242-*.md`
expected to return empty per slice 204 AC-7.
