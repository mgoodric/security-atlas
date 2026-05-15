# 056 — Hierarchical risk dashboard view — decisions log

Slice 056 is `Type: AFK` in its frontmatter — its acceptance criteria are
mechanically verifiable. But, exactly like slice 040, it surfaced genuine
build-time judgment calls: two acceptance criteria depend on backend
endpoints (and one query parameter) that do not exist on `main`, and the
heatmap's central data source — a `themes × org_units` count aggregation
— has no endpoint at all. This log records the calls in the
JUDGMENT-slice format (Decisions made · Revisit once in use · Confidence
per decision) so the maintainer can re-evaluate them once the dashboard
runs against a real, seeded platform. It does NOT block merge.

## Endpoint gap inventory (for follow-up backend slices)

Verified against `internal/api/` and `internal/api/httpserver.go` on
`main @ 356e529` at slice-build time:

| Panel / AC                  | What the AC wants                                                      | On main?                                                                                  | Slice 056 behavior                                                                              |
| --------------------------- | ---------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------- |
| Org tree structure (AC-2)   | `GET /v1/org_units`                                                    | yes (slice 053)                                                                           | fully bound — the parent/child tree is real and interactive                                     |
| Org tree risk counts (AC-2) | `GET /v1/org_units?include_risk_counts=true` — per-node severity chips | no — `ListOrgUnits` ignores all query params; `riskWire` (slice 019) has no `org_unit_id` | per-node chip renders a labelled "risk counts pending" affordance naming the missing param      |
| Theme heatmap axes (AC-3)   | themes (x) × org_units (y)                                             | yes — `GET /v1/themes` (slice 053) + `GET /v1/org_units` (slice 053)                      | fully bound — both real axes render, defaults ordered left of tenant-private themes             |
| Heatmap cell counts (AC-3)  | per-cell count + 5-step severity color                                 | no — there is no `themes × org_units` aggregation endpoint on main                        | `MissingEndpointPanel` banner names `GET /v1/risks/theme-heatmap`; cells render data-free       |
| Heatmap cell drill (AC-4)   | side panel of contributing risks, paginated                            | no — same missing aggregation endpoint                                                    | cell click opens a side panel that states the gap honestly; no fabricated risk list             |
| Heatmap cell tooltip (AC-5) | "nearest aggregation rule fires at {threshold}; window {window_days}d" | yes — `GET /v1/aggregation-rules` (slice 054)                                             | fully bound — tooltip cites REAL `min_risks` / `min_teams` / `window_days` from the rule        |
| Heatmap meta-risk icon      | mark a cell when a rule has fired                                      | no — depends on the missing cell-aggregation endpoint                                     | deferred with the cell counts; the side panel + tooltip carry the rule metadata in the meantime |
| Decision timeline (AC-6)    | `GET /v1/decisions` + `GET /v1/decisions/overdue`                      | yes (slice 055)                                                                           | fully bound — sorted list, overdue amber pills, all fields                                      |
| Timeline filter bar (AC-7)  | status / constraints / decision_maker / revisit_by range               | partial — `GET /v1/decisions` exposes only `?status=` and `?revisit_due_within_days=`     | `status` drives the server query; constraints / decision_maker / date-range filter client-side  |

A follow-up backend slice should scope:

1. **`GET /v1/org_units?include_risk_counts=true`** — each org_unit row
   carries an aggregate risk count broken down by severity band. This
   needs `risks.org_unit_id` (added in slice 052 schema) surfaced on a
   join. The slice-019 `riskWire` should also gain `org_unit_id`,
   `themes`, and a severity field so a risk-list row is self-describing.
2. **`GET /v1/risks/theme-heatmap`** — the `themes × org_units`
   aggregation: for each (theme, org_unit) cell, a risk count and an
   aggregate severity (the 5-step color), plus a `meta_risk_present`
   flag when an aggregation rule has fired for that cell within its
   window. This is the heatmap's central data source and the largest
   gap.
3. **A per-cell contributing-risk list endpoint** (or a `?theme=&org_unit=`
   filter on `GET /v1/risks`) — paginated, 25/page, returning each risk
   with its severity, theme tags, linked controls, and linked decisions
   (AC-4's side-panel contract).
4. **Optional:** richer server-side filters on `GET /v1/decisions`
   (`constraints[]`, `decision_maker`, `revisit_by` range) so the
   timeline filter bar can push all four dimensions to the server rather
   than filtering the returned page client-side.

## Decisions made

### 1. Build the heatmap's real axes + rule tooltips rather than waiting for the count endpoint

**Options considered:**

- **(A)** Render the entire heatmap panel as one `MissingEndpointPanel`
  placeholder until the `themes × org_units` aggregation endpoint ships
  — nothing real to show without cell counts.
- **(B)** Build the real CSS grid (org_unit rows × theme columns from the
  two endpoints that DO exist), render the cells data-free, overlay a
  `MissingEndpointPanel` banner for the counts, and bind the
  `aggregation-rules` endpoint so the cell-hover tooltips cite real
  thresholds.

**Chosen: (B).** The slice 040 / 041 → 060 / 064 precedent is "bind
everything that exists, name what doesn't." Two of the heatmap's three
data sources (theme vocabulary, org_unit list) and its tooltip metadata
source (aggregation rules) ARE on main — only the cell-count aggregation
is missing. Shipping the real axes makes the layout, the
default-vs-tenant theme ordering (AC-3), the cell-click side panel
(AC-4's interaction shell), and the rule-threshold tooltips (AC-5's
copy) all reviewable and exercised by the Playwright contract. When the
count endpoint lands, only the cell rendering changes — the grid,
ordering, side panel, and tooltips are done. **Confidence: high.**

### 2. Per-node risk-count chips: labelled affordance, not fabricated zeros

The org tree node was specified to show "risk count by severity
(color-coded chips)." With no endpoint and no client-side join path
(`riskWire` carries no `org_unit_id`), the only honest options were a
labelled "pending" affordance or nothing at all. A fabricated "0" per
band would read as real data and violate anti-criterion P0-1. The chip
renders `risk counts pending` with a `title` naming the missing query
param. **Confidence: high** — the anti-criterion is explicit.

### 3. Timeline filters: `status` server-side, the rest client-side

`GET /v1/decisions` exposes only `?status=` and
`?revisit_due_within_days=`. The filter bar needs four dimensions. Rather
than not shipping three of them, the panel pushes `status` to the server
when exactly one status is selected (narrowing the query + cache key)
and applies `constraints`, `decision_maker`, and the `revisit_by` range
client-side over the returned rows. This is honest — the panel never
claims a server capability it does not have — and correct for the
v1-scale data volumes the anti-criteria budget for (500 risks / 50
decisions). A follow-up backend slice can push all four server-side.
**Confidence: medium-high** — fine at v1 scale; revisit if a tenant's
Decision Log grows past a few hundred rows, at which point the
client-side page would need server-side pagination + filtering.

### 4. Filter state lives in the URL, derived during render (no `useState`)

AC-7 requires deep-linkable filter state. The filter state is decoded
from `useSearchParams()` during render and `router.replace()`'d on
change — there is no `useState` mirror and no `useEffect` seeding it
(React 19 set-state-in-effect lint, anti-criterion P0-5; slice 040
learned this). `useSearchParams` forces a `<Suspense>` boundary in the
App Router, hence the thin `RiskHierarchyPage` → `HierarchyView`
wrapper split. **Confidence: high** — matches the established slice 040
data-flow rule exactly.

### 5. AC-10 screenshots: capture procedure committed, images deferred

AC-10 wants 1440×900 light + dark screenshots in
`docs/images/risk-hierarchy/`. Capturing them requires a running `web`
dev server pointed at a running platform with a seeded tenant (org_units
in a hierarchy, the 10 default themes, an active aggregation rule,
decisions with past + future revisit dates) — the same live-backend
constraint slice 040 hit. This worktree has no running platform. The
slice commits `docs/images/risk-hierarchy/README.md` with the exact
capture procedure (viewport, theme toggle, the four panel states to
capture, the seed preconditions) and establishes the directory. The
actual PNGs are produced when the capture is run against a live
instance — slice 057 (README screenshots) consumes them and is the
natural place to run the capture. **AC-10 is PARTIAL** — procedure +
directory shipped, images pending a live instance. **Confidence:
high** — identical to the slice 040 precedent; the alternative
(committing fabricated or empty PNGs) is worse.

## Revisit once in use

- **Heatmap cell rendering** — once `GET /v1/risks/theme-heatmap` ships,
  swap the data-free cells for the 5-step severity scale + counts and
  wire the meta-risk icon. The grid, ordering, side panel, and tooltips
  do not change.
- **Org tree count chips** — once `?include_risk_counts=true` ships,
  replace the "pending" affordance with the color-coded severity chips.
- **Timeline filtering at scale** — if a tenant's Decision Log grows
  large, move `constraints` / `decision_maker` / `revisit_by` filtering
  and pagination server-side (decision #3).
- **Heatmap column count** — at very wide theme vocabularies the grid
  scrolls horizontally; if tenants routinely add many private themes,
  consider a column-virtualization or theme-group collapse affordance.
- **AC-10 images** — run the documented capture against a seeded live
  instance (slice 057) and commit the PNGs.

## Confidence summary

| Decision                                        | Confidence  |
| ----------------------------------------------- | ----------- |
| 1. Real axes + rule tooltips, defer cell counts | high        |
| 2. Labelled count affordance, no fabrication    | high        |
| 3. `status` server-side, rest client-side       | medium-high |
| 4. URL-derived filter state, no `useState`      | high        |
| 5. Screenshot procedure committed, images later | high        |
