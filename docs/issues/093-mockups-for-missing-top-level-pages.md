# 093 — Mockups for missing top-level pages (controls / evidence / risks / policies / audits / settings)

**Cluster:** Frontend / design
**Estimate:** 1d
**Type:** AFK

## Narrative

The UI ships pages for `/dashboard`, `/audit/[controlId]`, `/vendors`, `/board-packs`, `/catalog/scf`, `/risks/hierarchy`, `/controls/[id]`, and `/admin/*` — but the six most-discoverable top-level routes a user would naturally try (`/controls`, `/evidence`, `/risks`, `/policies`, `/audits`, `/settings`) all 404. Backend packages exist for every one of them (`internal/api/controls`, `evidence`, `risks`, `policies`, `policyacks`, `auditperiods`, plus settings-shaped surfaces under `admin*`). The gap is purely on the frontend, and it stops users from interacting with major portions of the platform they've already built.

The current convention has been **one slice per page**, each citing a wireframe mockup in `Plans/mockups/` as the design reference (see slices 040 dashboard, 041 control detail, 042 audit workspace, 043 board pack preview, 056 risk dashboard). Implementing six pages directly — without mockups first — would invite divergent UX choices across the six and an inconsistent app shell. This slice is the **design phase** that unblocks the six implementation slices that follow.

Output: six new HTML wireframes in `Plans/mockups/` matching the existing aesthetic (Tailwind via CDN, the `_shared/shell.css` partial, the same brand palette and chrome). Each mockup represents the canonical data model, primary actions, empty state, and loading state for its page. No real implementation work happens in this slice — the deliverable is six reviewable HTML files plus a short design-doc summarizing the navigation model and the cross-cutting decisions (where the new pages live in the top-nav, what the consistent table/filter/empty patterns are, what the settings page covers).

## Acceptance criteria

### One mockup per page (6 total)

- [ ] AC-1: `Plans/mockups/controls.html` — `/controls` list view. Shows the control library (anchors × frameworks), filterable by framework + scope + state (passing/drifted/exception). Empty state ("No controls match these filters"). Loading skeleton. Row click navigates to `/controls/[id]` (the detail view that already exists).
- [ ] AC-2: `Plans/mockups/evidence.html` — `/evidence` list view. Shows recent evidence records, filterable by control + freshness class + tenant scope. Empty state. Loading skeleton. Row click navigates to a per-record detail (out of scope for the mockup — placeholder link only).
- [ ] AC-3: `Plans/mockups/risks.html` — `/risks` list view (NOT the hierarchy at `/risks/hierarchy` — that's a separate existing page). Tabular list of risks with treatment, owner, residual band, last evaluated. Empty state. Loading skeleton. Row click navigates to a per-risk detail (placeholder).
- [ ] AC-4: `Plans/mockups/policies.html` — `/policies` list view. Shows the policy library with version, last updated, acknowledgment status (% of population that's acknowledged the current version). Empty state. Loading skeleton.
- [ ] AC-5: `Plans/mockups/audits.html` — `/audits` list view (plural — distinct from the existing per-control `/audit/[controlId]` workspace). Shows audit periods with framework + dates + sample counts + status. Empty state. Loading skeleton. Row click navigates to a period-detail page (placeholder).
- [ ] AC-6: `Plans/mockups/settings.html` — `/settings` page. **User-facing** settings only (theme, notifications preferences, profile, API tokens for personal CLI use). NOT admin-tenant settings — those already live at `/admin/*`. Cross-link from settings to admin shown as "Tenant administration → /admin" for users who have the admin role.

### Cross-cutting consistency

- [ ] AC-7: All six mockups load the same `_shared/shell.css` and use the same `brand-` palette + sticky top-bar + left-nav layout the existing `dashboard.html` and `board-pack.html` use. Visual review against the existing five mockups should show a coherent app, not six experiments.
- [ ] AC-8: `Plans/mockups/index.html` (the mockup gallery / index, if it exists; verify during implementation) is updated to link to the six new mockups under a "v1 fill-in pages" section, marked with `🚧 design only — implementation pending`.
- [ ] AC-9: The top-nav element across ALL existing AND new mockups is updated to reflect the new top-level routes (Dashboard · Controls · Evidence · Risks · Audits · Policies · Vendors · Board Packs · Settings · Admin). Same order across every mockup so a user navigating by mockup-flipping sees consistent placement.

### Design-decisions doc

- [ ] AC-10: `Plans/canvas/12-ui-fill-in-design-decisions.md` (or a sibling under the existing `Plans/canvas/` numbering) documents:
  - The chosen top-nav order + rationale
  - The standard empty-state + loading-skeleton patterns used in all six mockups (e.g. "empty state = centered illustration + 1 CTA; loading = 3 shimmer rows that match the table layout")
  - The decision on `/settings` scope (user-facing only; tenant/admin lives under `/admin`)
  - The decision on `/risks` vs `/risks/hierarchy` co-existence (list is canonical; hierarchy is a specialized view)
  - The decision on `/audits` vs `/audit/[controlId]` co-existence (plural is the period index; singular per-control workspace exists already)
- [ ] AC-11: A one-paragraph "next steps" section in the design-decisions doc enumerates the six follow-up implementation slices the design unblocks: `<NNN> — /controls list view`, `<NNN+1> — /evidence list view`, etc. Slot numbers are placeholders — assigned when those slices are written (post-merge of this slice).

## Constitutional invariants honored

- **CLAUDE.md "design before implement":** the existing per-page slices (040, 041, 042, 043, 056) each cited a mockup as their design reference. This slice keeps that pattern intact for the six remaining pages, rather than letting six engineers each invent their own UX choices in parallel.
- **CLAUDE.md "no Vercel/Next-template branding":** all six mockups must use the existing app shell + brand palette; no Tailwind defaults that drift toward "stock Vercel/shadcn page".
- **Slice 037 acceptance criterion AC-3 (default user can sign in + reach a usable workspace):** the missing pages are a major drag on this criterion. Mockups are step one of fixing it.

## Canvas references

- `Plans/mockups/` (existing aesthetic — match)
- `Plans/mockups/dashboard.html`, `board-pack.html`, `control.html` (closest reference points for chrome + table patterns)
- `Plans/canvas/10-roadmap.md` (verify the v1 page roster matches what this slice mocks — if the roadmap explicitly defers any of the six, drop that mockup AC and document why)
- Slice 040 (dashboard), 041 (control detail), 042 (audit workspace), 043 (board pack), 056 (risk hierarchy) — exemplars of the design→implement convention this slice extends

## Dependencies

- #005 (merged) — Next.js scaffold the mockups will eventually be implemented under
- #040, #041, #042, #043, #056 (merged) — exemplars to match the visual language of

## Anti-criteria (P0 — block merge)

- **P0-A1:** Does NOT implement any of the six pages in `web/app/`. The deliverable is HTML wireframes in `Plans/mockups/` + a design-decisions doc. Implementation work belongs to the six follow-up slices.
- **P0-A2:** Does NOT add new top-level pages beyond the six listed. If a sensible seventh emerges during design (e.g. `/users` for tenant self-service user management), file it as a separate slice — don't bolt it onto this one.
- **P0-A3:** Does NOT change any existing mockup beyond AC-9 (the nav update). If a mockup needs a visual refresh, that's a different slice.
- **P0-A4:** Does NOT introduce a new mockup tech stack (e.g. swap from Tailwind-via-CDN to Tailwind-via-build, swap from raw HTML to a Storybook setup, etc.). Match the existing tooling exactly.
- **P0-A5:** Does NOT invent backend endpoints that don't exist. Each mockup's data model must be derivable from the existing `internal/api/<package>/` handlers — verify by reading the relevant package's exported types before designing the table columns. If a mockup wants a column the backend doesn't return, the mockup is wrong, not the backend.
- **P0-A6:** Does NOT use Lorem Ipsum or filler copy. Use realistic placeholder data ("AC-1 — Logical access reviewed quarterly", "AWS IAM evidence — captured 2026-05-12", etc.) so the mockup's data shape is actually load-tested at design time.
- **P0-A7:** Does NOT bundle the six per-page implementation slices into this PR. They land separately, each citing this slice's mockup as their reference. The continuous-batch loop will pick them up automatically once their `_STATUS.md` rows land.

## Skill mix (3–5)

- HTML/Tailwind mockup authoring matching an existing visual language
- Reading Go handler signatures to derive the data model a UI page should expose
- Information architecture (top-nav order, page-scope decisions like `/settings` vs `/admin`)
- Empty-state + loading-skeleton design patterns
- Design-decisions documentation (capture choices so the implementing agents don't re-litigate them)

## Notes for the implementing agent

- For each mockup, BEFORE drawing the table, grep the backend package (`grep -rn "type.*struct" internal/api/<package>/`) to enumerate the actual fields the API returns. Build the table columns from that set, not from imagined fields. This is the single most-common mockup-vs-implementation mismatch source.
- The existing `dashboard.html` and `board-pack.html` are dense and feature-rich. The six new mockups are list-views — simpler. Don't over-design. Tabular list + filter sidebar + empty/loading states + sticky top-nav + footer is enough for each.
- Surfaced during the 2026-05-15 deploy-walkthrough session at `~/.claude/MEMORY/WORK/20260514-064726_security-atlas-unraid-deploy/`. The user noted six top-level routes returning 404 and proposed regenerating mockups for the missing pages then per-page implementation slices. This slice is the design half of that plan; six follow-up slices land the implementation half.
- After this slice merges and the design-decisions doc lands, the user (or the continuous-batch loop) will draft the six follow-up implementation slices. Each follow-up cites both this slice and the specific mockup file as its design reference. Estimated 1-2d per implementation slice, six total, batch-of-three throughput → ~6-8d wall-clock for the full UI fill-in.
