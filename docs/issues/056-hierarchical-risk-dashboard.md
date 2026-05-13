# 056 — Hierarchical risk dashboard view

**Cluster:** Frontend
**Estimate:** 3d
**Type:** AFK

## Narrative

The user-facing surface for the multi-level risk + Decision Log work in slices 52–054. Frontend view that lets a CISO or program lead navigate risks by `org_unit` × `level`, see theme heatmaps that surface emergent patterns, and watch decision-revisit dates approach.

Three primary panels:

1. **Org tree** — collapsible tree of `org_units`. Each node shows aggregate risk count by severity, with auto-aggregated meta-risks rendered distinctly from individual risks.
2. **Theme heatmap** — matrix of `themes × org_units`. Cell color = aggregate severity; cell value = count. Click a cell drills to the list of contributing risks. Hovering shows the rules that would fire (or have fired) at higher thresholds.
3. **Decision timeline** — chronological view of decisions with revisit-due markers. Filterable by `status`, `constraints[]`, `decision_maker`. Overdue revisits highlighted.

Visual design follows the existing mockup patterns (Tailwind + shadcn/ui). No new design system; this view consumes the patterns established in slice 040 (program dashboard) and extends them.

## Acceptance criteria

- [ ] AC-1: Route `/risks/hierarchy` renders the three-panel layout with org tree (left), theme heatmap (center), decision timeline (right). Responsive: stacks vertically below `md` breakpoint.
- [ ] AC-2: Org tree fetches from `GET /v1/org_units?include_risk_counts=true`. Each node shows: org_unit name, risk count by severity (color-coded chips), child-node count. Click expands; double-click drills to a filtered list view.
- [ ] AC-3: Theme heatmap rendered as a CSS grid (themes on x-axis, org_units on y-axis). Cell color from a 5-step severity scale; cell text = count. Empty cells show light gray. Built-in themes appear left of tenant-private themes.
- [ ] AC-4: Heatmap cell click opens a side panel listing contributing risks (paginated, 25 per page). Risks rendered with severity badge, theme tags, linked controls, linked decisions.
- [ ] AC-5: Heatmap cell hover shows a tooltip: "{count} risks; nearest aggregation rule fires at {threshold}; window {window_days}d." If a rule has fired, badge marks the cell with the meta-risk icon.
- [ ] AC-6: Decision timeline rendered as a vertical list, sorted by `decided_at` desc. Each row: `decision_id`, `title`, `decision_maker`, `decided_at`, `revisit_by`. Overdue revisits (`revisit_by < today`) highlighted with an amber border + "Revisit overdue" pill.
- [ ] AC-7: Filter bar above the timeline: `status` (multi-select), `constraints[]` (multi-select), `decision_maker` (typeahead), `revisit_by_range` (date range picker). Filter state persisted in URL query params (deep-linkable).
- [ ] AC-8: Empty states designed and shipped: empty org tree ("Add your first org_unit"), empty heatmap ("No themed risks yet"), empty timeline ("No decisions recorded yet"). Each with a primary action button linking to the relevant create flow.
- [ ] AC-9: Integration tests (Playwright) for: (a) loading the page with seeded data; (b) clicking a heatmap cell drills correctly; (c) decision timeline filtering by status updates URL and visible rows; (d) overdue decisions show the amber pill.
- [ ] AC-10: Screenshots captured (1440×900 viewport, both light + dark theme) and stored at `docs/images/risk-hierarchy/` — these feed slice 057 (README screenshots).

## Constitutional invariants honored

- **Invariant 4** (multidimensional scope) — UI never collapses `org_unit` and `scope_cell`; they're shown as distinct dimensions
- **Invariant 9** (manual is first-class) — manually-aggregated meta-risks render with the same visual weight as rule-driven; the source distinction shows in a small subscript, not in primary visual hierarchy
- **AI-assist boundary** — no AI-generated commentary on the dashboard without human-approval gating (out of scope for this slice anyway)

## Canvas references

- `Plans/canvas/06-risk.md §6.4` — risk hierarchy (org tree)
- `Plans/canvas/06-risk.md §6.5` — theme taxonomy (heatmap columns)
- `Plans/canvas/06-risk.md §6.6` — aggregation rules (heatmap cell tooltips)
- `Plans/canvas/06-risk.md §6.7` — Decision Log (timeline panel)

## Dependencies

- **005** (frontend bootstrap — Next.js + shadcn/ui)
- **053** (theme tagging API — heatmap data source)
- **054** (aggregation rules engine — meta-risk markers in heatmap)
- **055** (Decision Log CRUD — timeline data source)

## Anti-criteria (P0)

- Do NOT bundle org_unit and scope_cell visualizations — they're orthogonal concepts and conflating them confuses users (canvas invariant 4).
- Do NOT render rule-driven meta-risks as dominant over manual aggregations; they're peers (canvas invariant 9).
- Do NOT auto-acknowledge overdue decision-revisit reminders; the amber pill stays until a human takes action.
- Do NOT inline LLM-generated commentary on risk patterns; the dashboard surfaces data, not interpretations.
- Do NOT exceed 200ms p95 render time for the full three-panel view at 500 risks + 50 decisions; lazy-load the decision timeline beyond the first 25 if needed.

## Skill mix (3–5)

- `tdd` (Playwright integration tests for the three panels and drill-down flow)
- `engineering-advanced-skills:full-page-screenshot` (AC-10 screenshot capture; feeds slice 057)
- `engineering-advanced-skills:browser-automation` (Playwright fixtures + seeded data setup)
- `engineering-advanced-skills:performance-profiler` (AC-10 render budget validation)
- `simplify` (post-build refactoring pass on the heatmap rendering — likely the most complex piece)
