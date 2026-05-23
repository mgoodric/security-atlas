# 258 — Mockup index: 6 "design only — implementation pending" badges are stale; all six routes ship

**Cluster:** Quality / mockup hygiene
**Estimate:** 0.25d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 204 audit of `Plans/mockups/index.html`. The
mockup index has a "v1 fill-in pages (slice 093 · design only ·
implementation pending)" section. Each of the six tiles carries an
amber pill:

```
design only — implementation pending
```

As of 2026-05-23, all six tiles point at shipped, live routes. The pill
is factually wrong on every tile:

| Tile                        | Mockup href     | Live route                           | Implementing slice(s) |
| --------------------------- | --------------- | ------------------------------------ | --------------------- |
| Controls · list view        | `controls.html` | `web/app/(authed)/controls/page.tsx` | merged                |
| Evidence ledger · list view | `evidence.html` | `web/app/(authed)/evidence/page.tsx` | merged                |
| Risk register · list view   | `risks.html`    | `web/app/(authed)/risks/page.tsx`    | merged                |
| Policy library · list view  | `policies.html` | `web/app/(authed)/policies/page.tsx` | merged                |
| Audit periods · list view   | `audits.html`   | `web/app/(authed)/audits/page.tsx`   | merged                |
| Settings · personal         | `settings.html` | `web/app/(authed)/settings/page.tsx` | merged                |

The section heading text "slice 093 · design only · implementation
pending" is also stale.

The same 204 audit fleet that surfaced this finding has per-page
audits for all six of the named routes — they are real, live, and
under active maintenance. The mockup index just hasn't caught up.

The fix is two-token: remove the amber badge from each tile, and
either remove the "design only · implementation pending" qualifier
from the section heading or rename the section to reflect its current
character (e.g., "v1 list-view pages — implemented; see /controls
etc."). The implementing agent picks the exact rewording so long as
no tile carries a false "implementation pending" claim.

This is a Plans/-only mockup-doc edit. No production code change.

## Threat model

**Verdict.** **no-mitigations-needed.** The mockup index is a
contributor-facing developer artifact, not a shipped UI surface. The
file is static HTML under `Plans/mockups/` that no Next.js route
serves; the only way to view it is to open the file in a browser
locally. Removing inaccurate badges introduces no auth surface, no
data path, no external IO.

## Acceptance criteria

- **AC-1.** Each of the six tiles named in the table above renders
  without the "design only — implementation pending" pill.
- **AC-2.** The section heading text is updated so the parenthetical
  "(slice 093 · design only · implementation pending)" no longer
  appears. The implementing agent picks the new phrasing; one
  acceptable replacement is "v1 list-view pages · implementation
  shipped (slices 093 + follow-ons)" or simply "v1 list-view pages".
- **AC-3.** The hyperlink targets in each tile remain unchanged.
  Future-Claude that wants to scrub a tile entirely (because the
  mockup HTML lags the production page) files a separate slice; this
  slice ONLY removes the inaccurate badge + heading qualifier.
- **AC-4.** The intro paragraph beneath the section heading is also
  reviewed. The current text starts "Six top-level list-view pages
  the user naturally tries from the top-nav. Backend handlers exist
  for all six; these wireframes unblock the per-page implementation
  slices that follow." — the second sentence should be reworked so it
  no longer treats the six pages as forthcoming.
- **AC-5.** No other tiles or content in the mockup index are
  modified by this slice. The iteration-1 four-tile grid stays as is.
- **AC-6.** No Playwright / vitest test is added or modified — this
  is a documentation artifact, not a tested surface.
- **AC-7.** Pre-commit clean, DCO sign-off, Co-Authored-By trailer
  per CLAUDE.md.

## Constitutional invariants honored

This slice is documentation-only and does not touch any constitutional
invariant. It does honor:

- **Anti-pattern rejected (canvas §1.6):** "vanity trust centers" and
  forward-looking claims of capability the platform doesn't ship. The
  inverse applies here — the mockup index makes a deflationary claim
  ("implementation pending") about capability that DOES ship. Same
  honesty bug, mirrored direction.
- **CLAUDE.md "Editing Plans/ vs editing code":** mockups are
  reference, not production code. This slice edits the mockup as
  reference material to track shipped reality.

## Canvas references

- `Plans/canvas/12-ui-fill-in-design-decisions.md` — section 1
  documents the v2 top-nav including the six list-view pages this
  finding affects.
- `docs/audit-log/204-page-audit-index.md` — F-204-INDEX-1 source.

## Dependencies

- **#204** (per-page UI parity audit) — `in-progress`. This slice
  is one of the spillovers surfaced by the 204 audit fleet.
- **#093** (v1 fill-in mockups) — `merged`. The slice that added the
  six tiles + the "design only" qualifier this slice corrects.

## Anti-criteria (P0 — block merge)

- **P0-258-1.** Does NOT modify any production code (`web/app/*`,
  `internal/*`, `cmd/*`). The mockup index is at
  `Plans/mockups/index.html`; that file (plus this slice's docs/
  trail) is the only modification surface.
- **P0-258-2.** Does NOT modify other mockup HTML files. Each tile's
  destination mockup (e.g., `controls.html`) may itself be stale vs.
  the live route — but per-page mockup staleness is the subject of
  the corresponding 204 fleet audit, not this slice.
- **P0-258-3.** Does NOT add new tiles or surfaces. The "mockup
  index missing tiles for Calendar / Metrics / Vendors / Board Packs
  / Catalog · SCF / Admin" finding is filed as slice 259, a sibling
  spillover with its own scope.
- **P0-258-4.** Does NOT change tile hyperlinks. The six destination
  filenames stay byte-identical.
- **P0-258-5.** Does NOT update `_STATUS.md` or `CHANGELOG.md` in
  the same commit as the mockup edit — those updates are part of the
  parent 204 slice's bookkeeping, not this spillover's scope.

## Skill mix (3-5)

1. Mockup hygiene — `Plans/mockups/` as a design-doc artifact, not a
   shipped surface (per slice 183's precedent for mockup-stale
   corrections).
2. Tailwind + HTML — small static-HTML edit; remove an amber pill
   class block + update one section heading.
3. Honesty-audit follow-through — the slice 178 → slice 183/184/185
   pattern of treating "the UI claims X but the platform does Y" as a
   first-class quality finding.
