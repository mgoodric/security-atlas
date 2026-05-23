# 254 — Control-detail tab strip (Overview / Evidence / Mappings / Effective scope / Policies / Risks / History)

**Cluster:** Frontend / UI parity
**Estimate:** 1d
**Type:** JUDGMENT

## Narrative

Surfaced during slice 204's per-page audit of `/controls/{id}` (see
`docs/audit-log/204-page-audit-control.md`, Finding 2). The mockup
(`Plans/mockups/control.html` lines 139-152) places a sticky tab strip
directly under the page header with seven tabs:

- `Overview` (active by default)
- `Evidence` · count chip
- `Mappings` · count chip
- `Effective scope` · count chip
- `Policies` · count chip
- `Risks` · count chip
- `History`

The live page (`web/app/(authed)/controls/[id]/page.tsx`) has no tab
strip. All seven mockup tabs' content is collapsed onto a single
scrolling page — the Overview tab's content (KPI strip + Coverage table

- UCF graph + right rail), then the in-page sections that should be
  behind tabs (Evidence stream, Effective scope, Policies, Risks, Audit
  log).

**Why this matters:** the mockup's information density assumes a tabbed
view. Each tab's full content is page-sized (e.g., the Evidence tab is
the full evidence ledger filtered by `control_id` — hundreds of rows; the
Mappings tab is the full UCF-edges inspector for this control). Inlining
them on a single scrolling page works only because the live page renders
trivial empty-states for most of them (see Finding 1 / spillover #253).
Once #253 wires real backends and the lists populate, the scroll length
becomes untenable.

**Component reuse:** the codebase already ships a Tabs primitive
(`web/components/ui/tabs.tsx`, used in slice 044's
`/audits/[id]/workspace` via `web/components/audit/control-workspace.tsx`).
This is a composition slice, not a new-component slice.

## Threat model

**Verdict.** **no-mitigations-needed.** Routing-only change. No new
data path, no new auth surface. The decision the slice makes is
UX-shape (per-tab URL via search params vs hash vs sub-routes) — see
JUDGMENT notes below.

## Acceptance criteria

- **AC-1.** Seven tabs render under the page header per the mockup,
  in mockup order: Overview · Evidence · Mappings · Effective scope ·
  Policies · Risks · History. The tab strip is sticky under the
  top bar (matching `Plans/mockups/control.html` line 140
  `sticky top-12 z-10`).
- **AC-2.** Each tab's `count` chip (e.g., `Evidence 847`) reads
  from the relevant `useQuery` payload. When the query is loading
  or errored, the chip renders `—` (not a placeholder integer).
- **AC-3.** Overview tab content: KPI strip + Coverage table + UCF
  graph + Freshness card + Effective-scope summary. (Everything
  shared across audiences; the per-tab deep-dives live in
  subsequent tabs.)
- **AC-4.** Evidence tab: full evidence-stream pane (the section
  that ships in spillover #253 expands here, with filter pills +
  pagination + the row affordances).
- **AC-5.** Mappings tab: full coverage table + UCF graph (the
  Overview tab's summary; this tab is the deep-dive view per the
  mockup's "Open in mappings inspector →" target).
- **AC-6.** Effective scope tab: per-framework effective-scope
  breakdown (the right-rail summary on Overview becomes a full
  table here).
- **AC-7.** Policies / Risks / History tabs each render the
  corresponding full list (the same data spillover #253 ships
  to the Overview right rail; here they render full-page).
- **AC-8.** Tab state encoded in the URL (`?tab=evidence` or
  `#evidence` — JUDGMENT call, see below) so a deep link goes to
  the right tab. Browser back/forward navigates between tabs
  without re-fetching.
- **AC-9.** Playwright e2e covers: tab click navigates URL,
  refresh on a tab-deep-linked URL lands on the right tab,
  keyboard nav (arrow keys) moves focus between tabs per the
  shadcn Tabs primitive's a11y contract.

## Constitutional invariants honored

- **Invariant 1 (One control, N framework satisfactions).** The
  Mappings tab is the natural surface for the STRM edges that
  realize this invariant; collapsing it onto Overview hides the
  graph in the lower scroll.
- **Invariant 5 (FrameworkScope intersects).** The Effective
  scope tab gives the intersection its own dedicated surface,
  per canvas §5.5.
- **Anti-pattern rejected:** "vanity trust centers" — a tab
  strip with empty content is the canonical example; this slice
  depends on #253 wiring the backends so tabs are not vanity.

## Canvas references

- `Plans/canvas/03-ucf.md` — Mappings tab content
- `Plans/canvas/05-scopes.md` §5.5 — Effective scope tab
- `Plans/canvas/04-evidence-engine.md` — Evidence tab
- `docs/audit-log/204-page-audit-control.md` Finding 2

## Dependencies

- **#204** (UI parity audit) — parent.
- **#253** (stale endpoint empty-states) — soft dep; either order
  works. If #253 lands first, the tab strip absorbs the wired
  sections. If #254 lands first, the tabs render empty-states
  (the same empty-states #253 fixes).

## Anti-criteria (P0 — block merge)

- **P0-254-1.** Does NOT introduce a new component primitive.
  Reuse `web/components/ui/tabs.tsx`.
- **P0-254-2.** Does NOT lazy-load tab content with separate
  routes (`/controls/[id]/evidence` etc.) — that's a larger
  refactor. JUDGMENT call: URL-encoded tab state, single page.
- **P0-254-3.** Does NOT change the Overview tab's data layout.
  This is a re-foldering pass, not a redesign.

## JUDGMENT notes (for the implementing engineer)

The implementing engineer decides:

- **D1.** URL encoding for tab state — `?tab=evidence` (search
  param) vs `#evidence` (hash). Search param is more SEO-friendly
  and survives some redirects better; hash is simpler. Either is
  fine; record the choice in `docs/audit-log/254-tabs-decisions.md`.
- **D2.** Default tab on first visit — `Overview` per mockup
  parity, unless a strong reason to land on Evidence (the most
  trafficked tab on populated tenants). Default to Overview.
- **D3.** Count-chip rendering when the count is large (1000+) —
  abbreviated (`1.2k`) or full (`1,247`). Match the mockup
  (full numbers, formatted with comma thousands separator).

## Skill mix (3-5)

1. shadcn/ui Tabs primitive composition.
2. Next.js App Router search-param hydration (URL ↔ state).
3. Playwright tab-navigation a11y assertions.
4. Component re-foldering (moving in-page sections behind
   tab content boundaries without re-fetching).
