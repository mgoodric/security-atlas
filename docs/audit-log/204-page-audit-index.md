# 204 — Per-page UI parity audit: mockup index page

**Audited by:** slice 204 audit-fleet agent (index page)
**Audit date:** 2026-05-23
**Mockup file:** `Plans/mockups/index.html`
**Mockup title:** "security-atlas · mockups"
**Live URL audited:** `https://atlas-edge.home.gmoney.sh/`
**Slice number range used:** 258-259 (2 of 5 budgeted)

---

## Scope justification (lighter than other 204 fleet audits)

The mockup `index.html` is **not a product page**. It is a developer-facing
navigation grid that links to the other ten mockup HTML files under
`Plans/mockups/`. There is no live equivalent of the mockup index — the
root path `/` is a server-side redirect (slice 091) that routes:

- Authed users → `/dashboard` (audited in `204-page-audit-dashboard.md`)
- Unauthed users → `/login?from=/`

Per the assignment, reasonable audit axes for this special page are:

1. **Redirect honesty** — does `/` actually behave as slice 091 documents?
2. **Mockup-coverage gap** — does the mockup index reference features
   that have no backing mockup file or live route, OR does it omit
   features the platform actually ships?
3. **Marketing/branding parity** — does `/login` (the unauthed redirect
   target) render the chrome the user would expect from the mockup index
   (logo, tagline)?
4. **Dead links in mockup index** — do all `<a href>`s in the mockup
   index resolve to existing files?

This was budgeted as the lightest of the 11 fleet audits (~20 min, 1-3
findings expected). The audit produced **2 findings**, both in category
(iv) MOCKUP-STALE. Lower than the per-page average is consistent with
the lighter scope.

---

## Findings

### F-204-INDEX-1 — Mockup index falsely tags 6 implemented routes as "design only — implementation pending"

**Category:** (iv) MOCKUP-STALE
**Severity guess:** Low (operator-visible misinformation, not user-blocking)
**Spillover slice:** [#258](../issues/258-mockup-index-stale-design-only-badges.md)

The mockup index has six tiles in the "v1 fill-in pages (slice 093 ·
design only · implementation pending)" section. Each tile carries an
amber "design only — implementation pending" pill. As of 2026-05-23, all
six routes are implemented and live:

| Tile label                  | Mockup href     | Live route                           | Status      |
| --------------------------- | --------------- | ------------------------------------ | ----------- |
| Controls · list view        | `controls.html` | `web/app/(authed)/controls/page.tsx` | implemented |
| Evidence ledger · list view | `evidence.html` | `web/app/(authed)/evidence/page.tsx` | implemented |
| Risk register · list view   | `risks.html`    | `web/app/(authed)/risks/page.tsx`    | implemented |
| Policy library · list view  | `policies.html` | `web/app/(authed)/policies/page.tsx` | implemented |
| Audit periods · list view   | `audits.html`   | `web/app/(authed)/audits/page.tsx`   | implemented |
| Settings · personal         | `settings.html` | `web/app/(authed)/settings/page.tsx` | implemented |

Each of these has its own page audit in the same 204 fleet (this is the
parity audit that surfaced the staleness — the audited pages exist).

The mockup index also names the parent section "v1 fill-in pages (slice
093 · design only · implementation pending)" — the section heading
itself is stale. The badges and the heading should be removed (or
flipped to "implemented · see /controls etc"), depending on how slice
258 chooses to resolve.

---

### F-204-INDEX-2 — Mockup index omits 6 v1 top-nav surfaces shipped after slice 093

**Category:** (iv) MOCKUP-STALE (coverage gap)
**Severity guess:** Low (no user-facing breakage; the mockup index is a
developer/maintainer artifact, not a shipped UI)
**Spillover slice:** [#259](../issues/259-mockup-index-missing-post-093-tiles.md)

`Plans/canvas/12-ui-fill-in-design-decisions.md` §1 documents the v2
canonical top-nav, last revised 2026-05-16, which includes six surfaces
the mockup index does not represent at all:

| Top-nav item  | Canvas reference | Live route                  | Mockup tile? |
| ------------- | ---------------- | --------------------------- | ------------ |
| Calendar      | §1, slice 094    | `/calendar` (HTTP 200)      | NO           |
| Metrics       | §1, slice 097    | `/dashboards/metrics` (200) | NO           |
| Vendors       | §1               | `/vendors` (200)            | NO           |
| Board Packs   | §1               | `/board-packs` (200)        | NO           |
| Catalog · SCF | §1               | `/catalog/scf` (200)        | NO           |
| Admin         | §1               | `/admin` (200)              | NO           |

The board-pack PREVIEW is referenced in the iteration-1 grid (tile 03,
`board-pack.html`), but that's a per-pack preview view — not the
`/board-packs` list-view. The mockup index has no tile for the six new
list-view surfaces shipped between slices 094 and ~165.

This is not a user-visible breakage. The mockup index is a
developer/maintainer artifact (`Plans/mockups/index.html`) consumed by
contributors iterating on the design. But the iteration claim in the
index header (`"Ten screens covering the v1 high-leverage workflows"`)
becomes inaccurate once the v1 nav itself expands past those ten.

---

## Out-of-scope (verified clean)

These checks were performed and produced NO findings:

- **Redirect honesty.** `curl` against `/` confirms slice 091's contract
  end-to-end:
  - Authed (cookie present): `307 → /dashboard`, final 200.
  - Unauthed: `307 → /login?from=%2F`, final 200.
- **Login chrome (the unauthed redirect destination).** Slice 075's
  logo placement (top-of-card, `ThemeAwareLogo` per slice 176)
  renders correctly. Logo, tagline placement, and version footer
  (slice 072) all match the iteration-1 chrome the mockup index
  implies.
- **Dead links inside mockup index.** All 10 internal `<a href>`s
  resolve to existing files under `Plans/mockups/` (`dashboard.html`,
  `control.html`, `board-pack.html`, `questionnaire.html`,
  `controls.html`, `evidence.html`, `risks.html`, `policies.html`,
  `audits.html`, `settings.html`). The three footer links resolve to
  `Plans/ARCHITECTURE_CANVAS.md`, `Plans/UCF_GRAPH_MODEL.md`,
  `Plans/EVIDENCE_SDK.md` — all present. The canvas link inside the
  v1-fill-in section resolves to
  `Plans/canvas/12-ui-fill-in-design-decisions.md` — present.
- **Mockup file completeness.** `ls Plans/mockups/*.html` returns 11
  files matching the 10 tiles + index itself. No tile references a
  missing file.
- **Layout / chrome parity (the mockup-index has no live equivalent).**
  N/A by construction. The only "live equivalent" of `/` is the
  redirect target (`/login` or `/dashboard`); both are audited
  separately in the 204 fleet.
- **Data-bound surfaces (category iii).** N/A — mockup index is
  navigation only, no data fetches.

---

## Spillover count justification

The assignment budgeted 1-3 findings for this lighter audit. Two
findings is consistent with that range. Notably:

- The redirect honesty check produced **zero findings** — slice 091's
  contract is met.
- Login chrome (the redirect target) is the only live UI tied to this
  audit and was audited in detail by slices 075 + 176 already; no
  parity gap was surfaced.
- The two findings are both MOCKUP-STALE (category iv), both
  documentation-only fixes, both low-severity.

Slice numbers 260, 261, 262 are intentionally LEFT UNUSED. Filling the
budget when only two findings exist would dilute the per-slice signal
and create noise in the parallel-batch queue.

---

## Aggregate row (for `docs/audit-log/204-aggregate.md`)

| slice | page  | category | severity-guess | title                                                                                              |
| ----- | ----- | -------- | -------------- | -------------------------------------------------------------------------------------------------- |
| 258   | index | iv       | low            | Mockup index: 6 "design only" badges are stale; six routes ship                                    |
| 259   | index | iv       | low            | Mockup index: missing tiles for Calendar / Metrics / Vendors / Board Packs / Catalog · SCF / Admin |
