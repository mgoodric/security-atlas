# Decisions log — Slice 097 (Metrics dashboard + cascade-tree visualization)

This is a JUDGMENT slice (per `Plans/prompts/04-per-slice-template.md` "Slice types" + the slice's `Type: JUDGMENT` frontmatter). The slice ships the frontend on top of the slice-076 metrics-catalog backbone: a dashboard route, a cascade-tree explorer, a per-metric detail page, a manual-input modal, vitest tests for the cascade-reassembly + threshold-badge logic, and a Playwright e2e spec. This log records the design calls the engineer made rather than waiting for a maintainer sign-off.

## Decisions made

### D1 — Cascade tree renders vertically with indent-and-rule guides (HIGH confidence)

**Decision:** The cascade-tree explorer renders as a vertical, indented tree (parent above child, child indented 20px per depth level, with a left-edge vertical rule guide). Each row is a `Card`-themed list item with the metric name, latest value, and threshold badge. Clicking a node navigates to `/dashboards/metrics/[id]`.

**Alternatives considered:**

- **Horizontal tree** (parent on the left, children fanned out to the right). Rejected: the v1 catalog has Audit-readiness → 5 program-level children → up to 8 team-level grandchildren = up to 40 leaf nodes. A horizontal tree at that breadth either wraps awkwardly or forces horizontal scroll, both of which break the "morning glance" use-case named in the slice. Vertical scrolls naturally and matches every reader's mental model of an outline.
- **Force-directed graph** (e.g. d3-force, vis-network, react-flow). Rejected: pulls in a 50-100 KB dependency for a tree with maxDepth=3 and ~50 nodes — overkill. Force layouts are also non-deterministic across renders, making screenshot-based e2e regressions impossible.
- **shadcn `Accordion`** per parent. Considered seriously; the indent-and-rule shape generalises to N levels (the depth cap is 6) where Accordion only naturally nests one level cleanly. Rejected for that reason; the indent shape is also visually closer to a "tree" than a stack of disclosures.

**Confidence: HIGH.** Standard outline-tree UX, zero new deps, deterministic for screenshot regressions, generalises to MaxCascadeDepth=6 cleanly.

### D2 — Charts render as inline SVG primitives (no chart library) (MEDIUM-HIGH confidence)

**Decision:** Both the dashboard sparkline (90-day series) and the per-metric detail line chart use hand-rolled, inline SVG components colocated in `web/components/dashboards-metrics/`. Threshold overlays (target / warning / critical horizontal lines) are extra `<line>` elements at computed y-coordinates. No chart library is added to `web/package.json`.

**Alternatives considered:**

- **Recharts.** Considered (mature, declarative, ~95 KB gzipped). Rejected for this slice: adds a peer-dep on `d3` subsets, brings non-trivial SSR overhead on a Server Component-leaning route, and the slice's AC-15 explicitly says "No bespoke styling outside the shadcn theme" — Recharts's default look is not shadcn-themed and would need an override layer.
- **Visx.** Rejected: low-level Visx primitives are essentially "SVG + helpers" — the helper layer adds ~40 KB for what is one sparkline + one line chart. Build-time cost without enough surface to amortise over.
- **shadcn `Chart` component.** This is a Recharts wrapper in shadcn/ui as of mid-2026. Same bundle cost as Recharts; chosen against for the same reasons.
- **Tremor.** Rejected: nice charts but assumes Tailwind v3 conventions; we are on Tailwind v4 and shadcn theming, mixing the two is friction without sufficient payoff for two chart types.

The inline-SVG approach is ~50 lines of TypeScript per chart, uses CSS variables that already flow from the shadcn theme (`var(--primary)`, `var(--destructive)`, `var(--muted-foreground)`), and remains drift-free if the theme palette changes.

**Confidence: MEDIUM-HIGH.** If the dashboard grows past 2-3 chart types, revisit and adopt Recharts via shadcn's `Chart` wrapper to amortise the dep cost. Until then, keep dependency surface narrow.

### D3 — Admin-only "Submit value" button is gated by `getSessionMe().is_admin` from a TanStack Query (HIGH confidence)

**Decision:** The manual-input modal's trigger button is hidden unless `getSessionMe()` returns `is_admin: true`. The same client probe used by the board-pack approve gate (slice 043 decision D3) is reused — `/api/admin/me` returns `{ is_admin: boolean }` derived from a probe against `/v1/admin/credentials`. The platform itself still enforces the gate (the slice-076 handler returns 403 if `cred.IsAdmin` is false on POST `/v1/metrics/{id}/inputs`); the client gate is defense-in-depth + clearer affordance.

**Alternatives considered:**

- **Show the button always, surface 403 as an inline error.** Rejected: leaks affordance to non-admins, who would click and bounce. The slice 043 precedent already converged on the client gate; reuse keeps the pattern consistent.
- **Add a new `is_metric_admin` role.** Rejected: that is the slice-076 "deferred follow-on" (slice 076 decisions log D9). Out of scope here.
- **Server-render the gate via the (authed) layout.** Rejected: the layout already reads the session cookie for the existence check; teaching it to call `/v1/admin/credentials` couples a global concern to one page. The page-local probe is the right scope.

**Confidence: HIGH.** Direct pattern reuse from slice 043. The slice-076 handler enforces the real gate.

## Revisit once in use

- **D2 (chart library):** if the metrics surface grows to 5+ distinct chart types (heatmap, stacked bar, distribution, etc.), revisit adopting Recharts via shadcn's `Chart` wrapper. The amortisation tips in Recharts's favor once you have more than ~3 chart shapes.
- **D1 (tree layout):** if the catalog grows past ~150 metrics or maxDepth=6 is regularly hit, consider an explicit "collapse all but the path-to-here" interaction (current shape renders every node by default within the depth cap). At ~50 nodes this is not a problem.
- **D3 (admin gate):** when slice 076's `metric_admin` role-extension lands, swap the `is_admin` probe for the finer-grained role check. The UI surface (button hidden/shown) does not change.

## Confidence summary

| Decision                                   | Confidence  |
| ------------------------------------------ | ----------- |
| D1 — Vertical indent-and-rule cascade tree | HIGH        |
| D2 — Inline SVG charts (no chart library)  | MEDIUM-HIGH |
| D3 — Reuse `getSessionMe().is_admin` gate  | HIGH        |
