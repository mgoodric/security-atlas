# 258 — Mockup index stale "design only" badges drop · decisions log

**Slice:** `docs/issues/258-mockup-index-stale-design-only-badges.md`
**Branch:** `mockup/258-index-mockup-update`
**Author:** Claude (engineer subagent)
**Date:** 2026-05-23

This slice is `Type: AFK` per the spec frontmatter. The spec's ACs are
mechanically verifiable (six amber pills removed, section heading
qualifier dropped, intro sentence reworded). The orchestrator
nonetheless requested a decisions log capturing which tiles were
updated and which routes still legitimately carry the badge — useful
because the slice is one of the slice 204 audit-fleet spillovers and a
short audit trail keeps the fleet narrative coherent. This log is
intentionally compact.

---

## Decisions made

### D1 — Remove the entire badge wrapper `<div>`, not just the `<span>`

**Decision:** **Drop the entire `<div class="p-4 border-b
border-slate-100 flex items-center gap-2">…</div>` wrapper block from
each of the six tiles.** Leaving the wrapper and removing only the
inner `<span>` would render an empty padded row with a `border-b`
divider above each tile — a visible visual artifact (~64 px tall
empty band with a hairline rule) for no semantic content.

**Options considered:**

| Option                                                                       | Why rejected / why chosen                                                                                                                                                                                                                                                               |
| ---------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **Remove the entire `<div>` wrapper** — _chosen_                         | The wrapper exists only to host the badge (verified via `grep -A1`: every wrapper contains only the stale `<span>`). Removing it leaves the tile body (`<div class="p-5">`) flush against the outer `<a>` border, matching the iteration-1 four-tile grid above which has no badge row. |
| (b) **Remove only the `<span>`, keep the wrapper `<div>`**                   | Rejected — leaves a 64 px empty bordered band above each tile, defeating AC-1's spirit ("renders without the pill"). The pill is gone but its visual frame remains; a reader would correctly perceive the tile as "something was supposed to be here".                                  |
| (c) **Replace the badge with a "shipped" green pill** (positive-shaped flip) | Rejected — out of scope. The spec asks for badge removal, not badge replacement. A "shipped" pill on every tile would be marketing chrome on a contributor artifact, not the deflationary correction the spec requests.                                                                 |

**Rationale.** Verified via grep that the wrapper `<div>` contains
only the badge `<span>` (no other children). Removing the wrapper
preserves all hyperlinks (AC-3 honored — verified `grep -cE` returns 6) and produces a visually clean tile that matches the iteration-1
peer tiles above it.

**Confidence:** **high.** Verified by inspection that the wrapper
holds nothing but the stale badge.

### D2 — Section heading uses the spec's pre-approved replacement phrasing

**Decision:** **Use `v1 list-view pages` + mono qualifier
`implementation shipped (slices 093 + follow-ons)`.** This is the
literal first replacement the spec's AC-2 names.

**Options considered:**

| Option                                                                                     | Why rejected / why chosen                                                                                                                                                                                     |
| ------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **`v1 list-view pages · implementation shipped (slices 093 + follow-ons)`** — _chosen_ | Spec's first suggested replacement in AC-2; explicit and audit-traceable.                                                                                                                                     |
| (b) **`v1 list-view pages`** (the simpler alternative the spec also names)                 | Rejected as second-best — drops the slice-trail attribution. The mono qualifier line is small chrome but the slice-attribution is useful when a future contributor wonders "where did these tiles come from". |
| (c) **`v1 fill-in pages — implementation shipped`** (closer to original H2)                | Rejected — the phrase "fill-in pages" itself reads as forward-looking ("pages to be filled in"). Replacing with "list-view pages" matches the spec's intent and is what the tiles actually are.               |

**Confidence:** **high.** Spec pre-approved this exact phrasing.

### D3 — Intro paragraph rewording stays in the same paragraph shape

**Decision:** **Rework the second sentence to declare all six routes
shipped + reframe the wireframes as reference rather than scaffolding
for forthcoming work.** The first sentence (six pages from the
top-nav) is fine as-is; the second sentence (current: "Backend
handlers exist for all six; these wireframes unblock the per-page
implementation slices that follow") is the stale claim.

**New text:**

> Six top-level list-view pages the user naturally tries from the
> top-nav. All six routes are live in the Next.js app under
> `web/app/(authed)/`; these wireframes are the original design intent
> and remain useful as reference. See [design decisions](...) for
> top-nav order, empty-state / loading-skeleton patterns, and the
> /settings vs /admin scope decision.

**Confidence:** **high.** AC-4 explicitly authorizes the implementing
agent to rework the second sentence; the new text honors the shipped
reality while preserving the reference-value framing the spec
emphasizes.

### D4 — Tiles modified vs. badges legitimately retained

**Decision:** **All six tiles in the "v1 fill-in pages" section had
the badge removed. No tiles in the mockup index legitimately retain
the "design only — implementation pending" badge** as of this slice.
Every tile that previously carried the badge points at a shipped live
route (verified per the spec's table, all merged).

The iteration-1 four-tile grid above this section (dashboard,
questionnaire, control, audit) does NOT and never did carry the
"design only — implementation pending" badge — those tiles use a
different chrome convention (e.g., framework-name pill on the audit
tile) and are out of scope here. The spec's P0-258-2 anti-criterion
("no other mockup HTML files modified") covers per-tile staleness in
the destination mockups themselves (`controls.html` etc.); those are
separate slice 204 audit-fleet items.

**Tiles updated (6 of 6 in section):**

| Tile                        | href            | Badge removed | Hyperlink preserved |
| --------------------------- | --------------- | ------------- | ------------------- |
| Controls · list view        | `controls.html` | yes           | yes                 |
| Evidence ledger · list view | `evidence.html` | yes           | yes                 |
| Risk register · list view   | `risks.html`    | yes           | yes                 |
| Policy library · list view  | `policies.html` | yes           | yes                 |
| Audit periods · list view   | `audits.html`   | yes           | yes                 |
| Settings · personal         | `settings.html` | yes           | yes                 |

**Tiles NOT modified (per AC-5 + P0-258-3):**

- The iteration-1 four-tile grid (dashboard / questionnaire / control
  detail / audit). No `design only` badge ever existed on these.
- The MISSING tiles the spec mentions (Calendar / Metrics / Vendors /
  Board Packs / Catalog · SCF / Admin). These don't yet exist in the
  index; adding them is **slice 259's scope** per P0-258-3.

**Confidence:** **high.** Verified via `grep -n "design only|implementation pending"` against the final file: zero matches.

### D5 — Page-level intro line 35 left untouched

**Decision:** **Do NOT modify line 35 of `Plans/mockups/index.html`:**
`"…four iteration-1 deep workflows plus six v1 fill-in pages added in
slice 093."` The phrase "fill-in pages" in the page intro reads
slightly forward-looking but is factually true ("added in slice 093"
is a historical statement, not a status claim) and AC-5 says "no
other tiles or content in the mockup index are modified."

**Rationale.** AC-5 is explicit: only the badge wrapper, the section
heading qualifier, and the section intro paragraph (per AC-4) are in
scope. The page-level intro at line 35 is upstream of the section and
falls outside the scope. A separate slice can sweep the page-level
intro language if desired — flagging it in "Revisit" below.

**Confidence:** **high.** Anti-criteria are unambiguous.

---

## Revisit once in use

Specific items the maintainer should re-evaluate post-merge, in order
of expected priority:

1. **Slice 259 (missing tiles) lands.** P0-258-3 names the sibling
   spillover that adds Calendar / Metrics / Vendors / Board Packs /
   Catalog · SCF / Admin tiles. Once it merges, the section heading
   chosen here ("v1 list-view pages") may want a tweak to cover the
   broader set (the new tiles are also list-views but the "v1"
   modifier may become misleading if the new tiles are v2 scope).
   Suggested action when 259 lands: re-check the section heading
   phrasing fits both tile groups.
2. **Page-level intro line 35.** The phrase "six v1 fill-in pages
   added in slice 093" in the page-level intro is technically a
   historical statement but reads as forward-looking. A follow-up
   sweep slice could rephrase to `"…plus six v1 list-view pages
(shipped by slice 093 and its follow-ons)"`. Out of scope for 258
   per AC-5; file as a follow-up if it bugs the maintainer in review.
3. **Mockup vs. live drift per-tile.** Each of the six destination
   mockups (`controls.html`, `evidence.html`, etc.) may itself drift
   vs. the live `/controls`, `/evidence` route. The slice 204 audit
   fleet has per-page audits for these; the maintainer should ensure
   the per-page audit findings land as their own spillovers and not
   get folded into this slice's scope.
4. **Convention for "shipped" tile markers.** The mockup index no
   longer carries any status markers on tiles. If a future tile is
   added in a not-yet-shipped state, a convention will be needed
   (e.g., a green "shipped" pill vs. an amber "draft" pill). This
   slice's removal of the amber pill leaves an implicit convention:
   the mere absence of a status pill means "shipped." That's a tacit
   contract; the maintainer may want to document it explicitly the
   next time a forthcoming-tile addition surfaces.

---

## Confidence summary

| Decision                                                            | Confidence |
| ------------------------------------------------------------------- | ---------- |
| D1 — remove the entire badge wrapper `<div>`, not just the `<span>` | **high**   |
| D2 — section heading uses AC-2's pre-approved phrasing              | **high**   |
| D3 — intro paragraph 2nd sentence rewritten in-place                | **high**   |
| D4 — all 6 tiles modified; no tiles legitimately retain the badge   | **high**   |
| D5 — page-level intro line 35 left untouched per AC-5               | **high**   |

No `low` or `medium` confidence decisions. Every choice is either
spec-prescribed or verified by inspection.

---

## Anti-criteria honored

All five spec anti-criteria are honored:

- **P0-258-1.** No production code modified. Only `Plans/mockups/index.html`
  and this decisions log are touched.
- **P0-258-2.** No other mockup HTML files modified. The six destination
  mockups (`controls.html`, `evidence.html`, `risks.html`, `policies.html`,
  `audits.html`, `settings.html`) are byte-identical to their pre-slice
  state.
- **P0-258-3.** No new tiles or surfaces added. The MISSING tiles
  (Calendar / Metrics / Vendors / Board Packs / Catalog · SCF / Admin)
  are slice 259's scope.
- **P0-258-4.** Tile hyperlink targets unchanged. Verified via
  `grep -cE 'href="(controls|evidence|risks|policies|audits|settings)\.html"'`
  returns `6` against the post-edit file.
- **P0-258-5.** No `_STATUS.md` or `CHANGELOG.md` change in the slice
  commit.

The orchestrator's anti-criteria are also honored: no production code;
no `_STATUS.md`; no `CHANGELOG.md`; no other mockup files touched; no
sibling slice 259 work mixed in.
