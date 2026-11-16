- **docs(mockup):** slice 259 — mockup index now lists the six
  post-093 nav surfaces (Calendar, Metrics, Vendors, Board Packs
  list, Catalog · SCF, Admin) that ship live HTTP 200 routes but
  carry no iteration-1 HTML mockup. Updates `Plans/mockups/index.html`
  in two places: (1) the header paragraph no longer claims "ten
  screens covering the v1 high-leverage workflows" without
  qualification — it now explicitly names the six unmocked surfaces
  and anchor-links to the new section; (2) a new
  `<div id="unmocked">` section directly below the existing v1
  list-view grid presents the six surfaces as a three-column table
  (Top-nav item · Live route · Description) inside the same
  rounded-xl bordered-white-card motif used elsewhere on the page.
  Option B (header refresh + unmocked section) chosen over Option A
  (six new mockup HTML files + tiles) per the spec's preferred-path
  language — these surfaces have moved past the iteration-1 stage
  and fabricating after-the-fact HTML mockups for shipped React
  screens would be effort-without-leverage. The unmocked-section row
  for `/board-packs` explicitly disambiguates itself from the
  existing `board-pack.html` per-pack-preview deep-workflow mockup
  (AC-4). Decisions log at
  `docs/audit-log/259-mockup-index-missing-tiles-decisions.md` (D1
  A-vs-B, D2 placement-below-list-view, D3 table-shape, D4
  anchor-link). Pure docs edit — no production code touched
  (P0-259-1); existing ten mockup tiles untouched (P0-259-3); slice
  258's badge scope untouched (P0-259-4); `_STATUS.md` untouched
  (P0-259-5). Closes #259.
