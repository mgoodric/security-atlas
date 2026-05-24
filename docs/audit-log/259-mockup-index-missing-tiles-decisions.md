# 259 — Mockup index missing post-093 tiles · decisions log

**Slice:** `docs/issues/259-mockup-index-missing-post-093-tiles.md`
**Branch:** `quality/259-mockup-index-tiles`
**Author:** Claude (engineer subagent)
**Date:** 2026-05-23

This slice is `Type: AFK` per the spec frontmatter. The spec offers
two implementation paths (A: add six new HTML mockup files + tiles; B:
header refresh + a "Pages without iteration-1 mockups" section). The
spec itself names B as the preferred path for v1 and asks the
implementing agent to record the A-vs-B choice here.

This log is intentionally compact.

---

## Decisions made

### D1 — Take Option B (header refresh + unmocked section), not Option A (six new mockup files)

**Decision:** **Option B.** Update the index header so it no longer
claims "ten screens covering the v1 high-leverage workflows" without
qualification, then add a "Pages without iteration-1 mockups" section
listing the six post-093 nav surfaces with route + one-sentence
description in a definition-list-shaped table. Do NOT add new
`calendar.html` / `metrics.html` / `vendors.html` / `board-packs.html`
/ `catalog-scf.html` / `admin.html` files.

**Options considered:**

| Option                                                                                                        | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| ------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **(A) Add six new mockup HTML files + six new tiles**                                                         | Rejected for v1. The six surfaces (Calendar / Metrics / Vendors / Board Packs list / Catalog · SCF / Admin) have all shipped to live HTTP 200 routes in `web/app/(authed)/`. They have moved past the iteration-1 stage; the design-iteration loop the canvas imagines (iterate the HTML mockup → promote to React) is no longer the live workflow for these screens. Spinning up six self-contained Tailwind-via-CDN mockup files purely so the index can show six more tiles would be effort-without-leverage: nobody iterates an HTML mockup of a shipped React surface. The spec itself names B as preferred (lines 60-64) for exactly this reason — "the mockup index is a design-iteration artifact, and these six surfaces have moved past the iteration-1 stage". |
| **(B) Header refresh + unmocked section listing the six surfaces** — _chosen_                                 | Honest, low-effort, aligns the index with shipped reality without fabricating iteration-1 design artifacts after the fact. A future contributor who wants to redesign (say) the Metrics page can graduate that surface to a full Option-A tile at that time. The acknowledgment is explicit (P0-259 anti-pattern "no fabrication" honored — we don't pretend mockups exist for screens we never iterated on).                                                                                                                                                                                                                                                                                                                                                             |
| **(C) Hybrid: add mockups for the 1-2 surfaces a future contributor is most likely to redesign (e.g. Admin)** | Rejected. Speculative scope expansion. The spec's two paths are A or B; the orchestrator should not invent a middle path on a `0.5d` AFK slice. If a future contributor wants to iterate one of these surfaces, the unmocked-section table makes the gap visible and the path forward is obvious (add the file + add the tile).                                                                                                                                                                                                                                                                                                                                                                                                                                           |

**Rationale.** Option B satisfies AC-1 (header phrasing fixed),
AC-3 (new unmocked section in place), AC-4 (board-pack vs board-packs
disambiguation present — the unmocked-section row explicitly links to
`board-pack.html` and calls out the per-pack-preview distinction),
AC-5 (this log), AC-6 (no other tiles modified), and AC-7 (pre-commit
plus DCO plus Co-Authored-By trailer). AC-2 is Option-A-only and
therefore N/A.

**Confidence:** **high.** The spec's preferred-path language is
unambiguous; B is the operative answer.

### D2 — Place the unmocked section BELOW the v1 list-view section, not above

**Decision:** **Insert the new "Pages without iteration-1 mockups"
section directly below the existing "v1 list-view pages" grid, ABOVE
the "How to iterate" closing note.** The reading order becomes:
(1) iteration-1 four-tile grid → (2) v1 list-view six-tile grid →
(3) unmocked six-surface table → (4) "How to iterate" closing note.

**Rationale.** Honors the existing visual cadence: tile-style cards
first, then the table (a degraded presentation reflecting that these
surfaces lack iteration-1 design artifacts), then meta-instructions.
Placing the unmocked section above the v1 list-view grid would put a
table between two tile grids and break the visual rhythm.

**Confidence:** **high.** Standard visual-hierarchy reading.

### D3 — Use a `<table>` for the unmocked section, not tile cards or a `<dl>`

**Decision:** **Use a single `<table>` with three columns (Top-nav
item, Live route, Description) styled to match the rounded-xl bordered
white card pattern used elsewhere on the page.** AC-3 explicitly
permits "a simple definition-list or table presentation".

**Options considered:**

- **Tile-style cards (Option A's visual shape):** rejected — would
  imply mockups exist; misleading.
- **Plain `<ul>` or `<dl>`:** functional but visually inconsistent
  with the rest of the index, which uses bordered white cards
  throughout.
- **`<table>` inside a rounded bordered card** — _chosen_. Three
  scannable columns; matches the white-card-with-slate-200-border
  motif of the surrounding sections without misrepresenting the
  surfaces as iteration-1 mockups.

**Confidence:** **high.**

### D4 — Add an anchor link from the header to the new section

**Decision:** **Add `id="unmocked"` on the new section's outer
`<div>` and a `<a href="#unmocked">unmocked section</a>` hyperlink in
the rewritten header paragraph.** Makes the cross-reference clickable
rather than asking the reader to scroll.

**Rationale.** Index-page UX cue. The header explicitly mentions the
six surfaces; a contributor who reads "see the unmocked section below"
benefits from a direct jump-link. Negligible cost.

**Confidence:** **high.**

---

## Verified targets

The slice's narrative table claims six surfaces have NO mockup file
under `Plans/mockups/`. Verified with `ls Plans/mockups/`:

| Surface          | Expected file path               | Exists? |
| ---------------- | -------------------------------- | ------- |
| Calendar         | `Plans/mockups/calendar.html`    | NO      |
| Metrics          | `Plans/mockups/metrics.html`     | NO      |
| Vendors          | `Plans/mockups/vendors.html`     | NO      |
| Board Packs list | `Plans/mockups/board-packs.html` | NO      |
| Catalog · SCF    | `Plans/mockups/catalog-scf.html` | NO      |
| Admin            | `Plans/mockups/admin.html`       | NO      |

All six confirmed missing. The slice's claim is accurate.

The existing `Plans/mockups/board-pack.html` (singular) is the
per-pack preview deep-workflow mockup and remains untouched. The
unmocked-section row for `/board-packs` calls out this distinction
explicitly to satisfy AC-4.

---

## Tiles added / DEFERRED

Per Option B, NO new tile-style cards were added. The six surfaces
appear as table rows in the new "Pages without iteration-1 mockups"
section. If a future contributor wants to graduate one of these
surfaces to a full Option-A tile (e.g. while redesigning the Metrics
page), they should:

1. Create the HTML file in `Plans/mockups/` matching the surrounding
   convention (Tailwind via CDN, `brand:` color tokens, the canonical
   sidebar partial duplicated from `dashboard.html`).
2. Remove the corresponding row from the unmocked-section table.
3. Add a new tile to the v1 list-view grid following the existing
   `<a href="<file>.html" class="group block bg-white rounded-xl …">`
   pattern.

---

## Files touched

- `Plans/mockups/index.html` — header paragraph rewritten; new
  `<div id="unmocked">…</div>` section inserted between the v1
  list-view grid and the "How to iterate" note.
- `docs/audit-log/259-mockup-index-missing-tiles-decisions.md` — this
  file (AC-5).
- `CHANGELOG.md` — one `### Changed` bullet under `## Unreleased`
  describing the index update (per the spec's "CHANGELOG.md bullet
  under `### Changed`" requirement in the implementation brief).

Per P0-259-5, `docs/issues/_STATUS.md` is NOT touched in this commit.
Per P0-259-1, no production code (`web/app/*`, `internal/*`,
`cmd/*`) is touched. Per P0-259-3, the existing ten mockup files'
content is unchanged. Per P0-259-4, the six "design only" badges
from slice 258 are not modified by this slice — that's slice 258's
scope.
