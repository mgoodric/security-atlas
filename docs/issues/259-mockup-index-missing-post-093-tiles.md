# 259 — Mockup index: missing tiles for Calendar / Metrics / Vendors / Board Packs / Catalog · SCF / Admin

**Cluster:** Quality / mockup hygiene
**Estimate:** 0.5d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 204 audit of `Plans/mockups/index.html`. The
mockup index header claims:

> Ten screens covering the v1 high-leverage workflows for a solo
> security leader running their entire program from one tool — four
> iteration-1 deep workflows plus six v1 fill-in pages added in
> slice 093.

That claim was accurate when slice 093 landed. It is no longer
accurate as of 2026-05-23. Between slices 094 and ~165, the v1 nav
expanded to include six additional top-level surfaces, all of which
ship live HTTP 200 routes today but have **zero presence** in the
mockup index:

| Top-nav item            | Source                   | Live route            | Mockup file under Plans/mockups/                                               | Tile in index? |
| ----------------------- | ------------------------ | --------------------- | ------------------------------------------------------------------------------ | -------------- |
| Calendar                | slice 094 / canvas §12.1 | `/calendar`           | none                                                                           | NO             |
| Metrics                 | slice 097 / canvas §12.1 | `/dashboards/metrics` | none                                                                           | NO             |
| Vendors                 | canvas §12.1             | `/vendors`            | none                                                                           | NO             |
| Board Packs (list view) | canvas §12.1             | `/board-packs`        | none (the existing `board-pack.html` is a per-pack PREVIEW, not the list view) | NO             |
| Catalog · SCF           | canvas §12.1             | `/catalog/scf`        | none                                                                           | NO             |
| Admin                   | canvas §12.1             | `/admin`              | none                                                                           | NO             |

The mockup index is a contributor/maintainer artifact at
`Plans/mockups/index.html` consumed by anyone iterating on the design
(per the index's own intro paragraph: "Click into any screen, then
iterate via the canvas docs and the source files in Plans/mockups/").
A contributor who reads the index gets a substantially incomplete
picture of the v1 UI surface area.

This finding is distinct from slice 258 (which corrects six tiles
that DO exist in the index but carry stale "implementation pending"
badges). This one ADDS missing tiles or re-frames the section.

The implementing agent has a choice:

- **Option A (full-fidelity):** add six new mockup HTML files
  matching the live routes, then add six tiles to the index. Higher
  effort, higher value — the design-iteration loop the canvas
  imagines (iterate the HTML mockup, then promote to React) becomes
  available for the new surfaces.
- **Option B (header refresh only):** rewrite the index header so it
  no longer claims "ten screens covering the v1 high-leverage
  workflows", and add a "Pages without mockups (implementation
  shipped)" section that lists the six surfaces by route + brief
  description, with the explicit acknowledgment that they don't have
  iteration-1 HTML mockup files. Lower effort, lower value — but
  honest.

The preferred path is **Option B** for v1 (the mockup index is a
design-iteration artifact, and these six surfaces have moved past the
iteration-1 stage). If a future contributor wants to add Option A
mockup files for one of these surfaces (e.g., when redesigning the
metrics page), they can graduate that surface to a full tile at that
time.

The implementing agent picks A vs B and records the choice in the
slice's decisions log.

## Threat model

**Verdict.** **no-mitigations-needed.** The mockup index is a
contributor-facing developer artifact, not a shipped UI surface. No
auth surface, no data path, no external IO. Adding HTML files to
`Plans/mockups/` (Option A) introduces no security boundary — those
files are not served by any Next.js route or static-asset handler.

## Acceptance criteria

- **AC-1.** The mockup index header paragraph is updated so it no
  longer claims "ten screens covering the v1 high-leverage
  workflows" without qualification. Acceptable replacements:
  - "Sixteen screens covering the v1 nav surfaces..." (if Option A
    is taken and all six get new mockup tiles)
  - "Ten iteration-1 mockup screens; six additional v1 surfaces ship
    without iteration-1 mockups — see the unmocked section below"
    (Option B)
- **AC-2.** **(Option A only)** Six new mockup HTML files are added
  to `Plans/mockups/`, named to match their live route:
  `calendar.html`, `metrics.html`, `vendors.html`, `board-packs.html`
  (distinct from the existing `board-pack.html` per-pack preview),
  `catalog-scf.html`, `admin.html`. Each file is self-contained
  Tailwind-via-CDN HTML matching the existing mockup convention
  (see e.g. `controls.html`).
- **AC-3.** **(Option B only)** A new section "Pages without
  iteration-1 mockups" appears below the existing v1-fill-in section.
  It lists the six surfaces by route + one-sentence description,
  matching the table in this slice's Narrative. No tile-style cards;
  a simple definition-list or table presentation is fine.
- **AC-4.** **(Both options)** If `board-packs.html` is added
  (Option A), it is explicitly distinct from `board-pack.html`. The
  index disambiguates them — `board-pack.html` is the per-pack
  preview, `board-packs.html` is the list view.
- **AC-5.** **(Both options)** The slice's decisions log captures
  the A-vs-B choice and the reasoning. File at
  `docs/audit-log/259-mockup-index-missing-tiles-decisions.md`.
- **AC-6.** No other tiles or content in the mockup index are
  modified by this slice. The iteration-1 four-tile grid stays as
  is. The six "design only" tiles are slice 258's scope.
- **AC-7.** Pre-commit clean, DCO sign-off, Co-Authored-By trailer
  per CLAUDE.md.

## Constitutional invariants honored

This slice is documentation-only and does not touch any constitutional
invariant. It does honor:

- **Anti-pattern rejected (canvas §1.6):** the mockup index's "ten
  screens covering the v1 high-leverage workflows" framing carries a
  subtle UI-honesty bug — it understates the v1 surface area. A
  contributor reading the index alone would not know Calendar /
  Metrics / Vendors / etc. exist as first-class v1 pages. The fix
  aligns the doc with shipped reality.
- **CLAUDE.md "Editing Plans/ vs editing code":** mockups are
  reference, not production code. This slice edits the mockup index
  as reference material to track shipped reality.

## Canvas references

- `Plans/canvas/12-ui-fill-in-design-decisions.md` §1 — v2
  canonical top-nav order, last revised 2026-05-16 with the
  Calendar / Metrics / Catalog · SCF additions.
- `Plans/canvas/13-ui-mockup-audit-2026-05-16.md` — prior mockup-
  audit pass that flagged the post-093 nav drift but did not file a
  slice for the index page itself.
- `docs/audit-log/204-page-audit-index.md` — F-204-INDEX-2 source.

## Dependencies

- **#204** (per-page UI parity audit) — `in-progress`. This slice is
  one of the spillovers surfaced by the 204 audit fleet.
- **#094** (compliance calendar) — `merged`. Source of the Calendar
  nav entry this slice surfaces as missing from the mockup index.
- **#097** (metrics dashboard cascade) — `merged`. Source of the
  Metrics nav entry.
- **#258** (mockup index: stale "design only" badges) — `ready`.
  Sibling spillover from the same audit. The two should be merged
  in either order; they touch the same file but different sections.

## Anti-criteria (P0 — block merge)

- **P0-259-1.** Does NOT modify any production code (`web/app/*`,
  `internal/*`, `cmd/*`). Only `Plans/mockups/` and `docs/`.
- **P0-259-2.** **(Option A constraint)** Does NOT introduce
  Tailwind-config drift across mockup files. New mockup HTML uses
  the same `tailwind.config` block as the existing files (the
  `brand:` color tokens).
- **P0-259-3.** Does NOT modify the existing ten mockup files'
  content. New files added (Option A) are separate; index-page text
  changes are limited to header paragraph + new section addition.
- **P0-259-4.** Does NOT touch the six "design only" badges from
  slice 258. That's slice 258's scope; this slice and 258 must
  merge independently.
- **P0-259-5.** Does NOT update `_STATUS.md` or `CHANGELOG.md` in
  the same commit as the mockup edit — those updates are the parent
  204 slice's bookkeeping, not this spillover's scope.
- **P0-259-6.** If Option A is taken, the new HTML files MUST be
  self-contained (no shared build, no npm import). The existing
  `_shared/shell.css` may be used; no other shared assets.

## Skill mix (3-5)

1. Mockup hygiene — `Plans/mockups/` as a design-doc artifact;
   keeping it aligned with the canvas-documented v1 nav.
2. Tailwind + static HTML — either six new self-contained mockup
   files or a structural addition to `index.html`.
3. Canvas-cross-reference reading — keeping the index's framing
   honest against `canvas/12-ui-fill-in-design-decisions.md` §1.
4. Decisions-log discipline — recording the A-vs-B implementation
   choice so future contributors don't re-litigate.
