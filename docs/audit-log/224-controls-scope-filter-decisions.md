# Slice 224 — Controls Scope filter pill (decisions log)

**Type:** JUDGMENT
**Spec:** `docs/issues/224-controls-list-scope-filter-pill.md`
**Status at slice close:** functionally complete on the slice branch;
backend + BFF + UI wired; vitest + Go unit coverage green; Playwright spec
extended (quarantined per the existing slice 098 pattern).

This log records the subjective design calls made during build, so the
maintainer can iterate post-deployment without reverse-engineering the
PR diff.

---

## D1 — 50-cell cap rationale

**Decision.** The Scope filter pill renders up to 50 cell entries in
the dropdown. When the tenant has more than 50 cells, an `<Alert>`
banner above the table announces "Scope filter capped at 50 cells";
the first 50 cells (newest-first from `GET /v1/scopes/cells`) are
selectable.

**Rationale.** AC-5 specifies the cap and a typeahead replacement for
larger tenants. The cap protects the page from a dropdown that runs
the length of the viewport; the banner keeps the UI honest so the
operator knows their universe is truncated. The newest-first ordering
matches the SQL (`ORDER BY created_at DESC` in `ListScopeCells`),
which biases toward recently-active cells — the realistic case where
50 is sufficient.

**Confidence:** medium. The cap is conservative; if real tenants hit
it routinely, the follow-on typeahead becomes urgent. If they never
hit it, the banner stays dormant and the cap is a non-issue.

**Revisit once in use.** Once a tenant in v2 reaches >50 cells,
revisit the typeahead UX (in-input search + lazy load + keyboard nav)
rather than raising the cap.

---

## D2 — Server-side filtering, not client-side

**Decision.** Scope intersection happens server-side in the
`worst_per_anchor` CTE inside `ListSCFAnchorsLatestWithState` (and the
`ForVersion` sibling). The frontend never receives the underlying
`applicability_expr`; only the BFF forwards a `?scope=<cell_id>`
query string to the upstream.

**Rationale.** P0-224-2 explicitly forbids pushing
`applicability_expr` to the browser — it would expose business logic
(which controls apply to which scopes, framework-by-framework). The
server-side intersection also stays inside RLS: out-of-tenant cell
ids return zero rows naturally (no 404 leak), per the threat-model
Spoofing row in the spec.

The SQL pattern is the standard "optional filter via null UUID
sentinel" — `WHERE ($N::uuid IS NULL OR le.scope_cell_id = $N::uuid)`
— so a single query covers both the no-filter and filter branches.
This keeps the worst_per_anchor CTE as a single point of truth (the
sibling slice-104 query) and means the rollup math doesn't fork
between paths.

**Confidence:** high. Pattern matches the existing slice-104 design.

**Revisit once in use.** Watch the query plan once a tenant has
thousands of `control_evaluations` rows — the planner may want a
partial index on `(tenant_id, scope_cell_id)` if scope-filtered reads
dominate. Not needed at current scale.

---

## D3 — Typeahead deferred

**Decision.** The 50-cell cap is the v0 UX. A typeahead-search input
that replaces the static dropdown when a tenant exceeds 50 cells is
deferred to a follow-on slice.

**Rationale.** Per the spec's AC-5 ("defer the typeahead's full UX to
a follow-on slice if estimate exceeds 1d"), the cap-plus-banner
shape ships v0 honestly: the operator can see the universe is
truncated, and the dropdown still works for the most-likely-recent
cells. The typeahead is a richer-but-orthogonal UX problem (keyboard
nav, debounced filtering, optional fuzzy-match) and lands as its own
slice when a real tenant actually hits the cap.

**Confidence:** high. No evidence yet of tenants exceeding the cap;
shipping a typeahead now would be premature.

**Revisit once in use.** First tenant to exceed 50 cells triggers a
follow-on slice. Or, if the bootstrap seed eventually populates more
than ~10 cells, lower the cap-banner threshold so the typeahead's
absence is visible to maintainers sooner.

---

## D4 — Cell label fallback

**Decision.** When a scope cell has no explicit `label` text on the
wire (the column is `NOT NULL DEFAULT ''` per slice 017 migration),
the dropdown renders a deterministic `k=v / k=v` summary of the cell's
`dimensions` JSONB. Empty-dimensions cells fall back to the cell's
UUID.

**Rationale.** The bootstrap seed sets `label = ''` for the default
cell; presenting an empty option in the dropdown would be a UX bug.
The `k=v / k=v` summary mirrors how the spec's threat-model description
talks about cells ("`prod / aws / us-east`") and stays deterministic
across re-renders (sorted by dimension name).

**Confidence:** high. Matches the spec example.

**Revisit once in use.** If tenants start labelling cells routinely,
the fallback is dead code — leave it as defense-in-depth.

---

## D5 — Banner above the table, not inline with pills

**Decision.** The cap banner renders above the table inside the
`ListPage` body, NOT inline with the FilterPills row.

**Rationale.** The FilterPills row is a horizontal flex layout; an
inline `Alert` would either wrap awkwardly or push the meta line
("Showing N of M") off-screen. Placing it above the table keeps the
pill row's compact shape intact and gives the banner the breathing
room a multi-line message wants. The banner is one-row dismissable-
feeling (default `Alert` variant — informational, not destructive).

**Confidence:** medium. The placement could be improved by a future
designer pass; for v0 it is correct and unobtrusive.

**Revisit once in use.** First time a maintainer sees the banner
fire on a real tenant, eyeball the layout and either approve or move
the banner above the entire ListPage (which would shift the page-
header down — probably worse).

---

## D6 — Backend extends existing queries, doesn't add new ones

**Decision.** The scope filter is added as a 3rd (or 4th, for the
ForVersion variant) parameter to the **existing** sqlc queries
`ListSCFAnchorsLatestWithState` and `ListSCFAnchorsForVersionWithState`.
NOT new "...Scoped" sibling queries.

**Rationale.** The worst_per_anchor CTE is identical between the
filtered and unfiltered branches; duplicating the giant query would
double the surface to keep in sync (the slice-104 + slice-159 typing
notes plus this slice's new filter). The null-UUID sentinel pattern
(`$N::uuid IS NULL OR le.scope_cell_id = $N::uuid`) is well-supported
by pgx and lets the planner optimise the no-filter branch to the
same plan it had before.

The hand-maintained `internal/db/dbx/scf_anchors.sql.go` adapter was
updated to match (per the slice-104 "regen-on-rebase" note in the
file's NOTE comment).

**Confidence:** high. The pattern is the same one slice 002 used for
its filterable scope queries.

**Revisit once in use.** If sqlc regen-on-rebase ever drops the
hand-maintained file, the planner plan and the new parameter shape
need to be verified against the regenerated output. The
`internal/db/queries/scf_anchors.sql` source-of-truth file is kept
in sync — the regen would emit the same parameter shape.

---

## D7 — `/api/scope-cells` is a new tiny BFF route, not a query param on `/api/controls`

**Decision.** A new BFF route `web/app/api/scope-cells/route.ts`
proxies `GET /v1/scopes/cells`. It does NOT live as a sub-query on
`/api/controls` (the page does two separate `useQuery` calls).

**Rationale.** The page needs the cell list to populate the Scope
pill's dropdown options regardless of whether the user has narrowed
by scope yet. Bundling it into `/api/controls` would force the cell
list to load synchronously with the anchor list (slower first
render) and would couple the two query lifetimes (re-fetching the
anchor list would also re-fetch the cell list). Two queries keep the
caching independent.

**Confidence:** high. Matches the sibling-list-view BFF route
convention (`/api/risks`, `/api/exceptions`, etc. — one route per
shaped dropdown).

**Revisit once in use.** If a v2 page needs a richer scope-cells API
(filters, search, pagination), the route's query params expand
naturally without re-architecting `/api/controls`.

---

## D8 — Frontend never narrows by scope client-side

**Decision.** The `applyFilters` function in
`web/app/(authed)/controls/filters.ts` does NOT examine `filters.scope`.
The narrowing is exclusively server-side via the BFF query.

**Rationale.** Same reason as D2 — the client never gets
`applicability_expr`, so it cannot reason about cell membership.
Adding a client-side narrowing pass would be redundant with the
server's narrowing AND would silently break when the wire shape
evolves. The cleanest design is "the server's result is the truth"
— `applyFilters` only consumes columns the wire already carries
(`anchor.family`, `state.result`, `state.freshness_status`).

**Confidence:** high. Matches AC-6 in the spec verbatim.

**Revisit once in use.** Never — this is a constitutional invariant
of the design (P0-224-2).

---

## D9 — CI-delta scan results

**Method.** Greppped for callers of `ListSCFAnchorsLatestWithState`
/ `ListSCFAnchorsForVersionWithState` (added a Params field) and
callers of `listAnchorsWithState` (added an optional arg) and
`fetchControlsList` (added an optional arg).

**Findings.**

- `ListSCFAnchors{Latest,ForVersion}WithStateParams`: ONE caller —
  `internal/api/anchors/handlers.go`. Updated. Other test files
  reference only the Row types, which are unchanged.
- `listAnchorsWithState`: ONE caller — the BFF
  `web/app/api/controls/route.ts`. Updated to pass through the
  optional scope.
- `fetchControlsList`: TWO callers — the /controls page (now passes
  scope) and `web/app/(authed)/evidence/page.tsx` (wrapped in arrow
  `() => fetchControlsList()` so the TanStack queryFn typing stays
  happy). Functionally a no-op for evidence (no scope filter on
  /evidence in v0).

**Pre-existing typecheck baseline.** Before slice 224: 16 typecheck
errors (all pre-existing, surfaced by Next 16 + React 19 strict-mode
upgrades). After slice 224: 15 typecheck errors — the fix to
`evidence/page.tsx`'s `queryFn: fetchControlsList` to
`queryFn: () => fetchControlsList()` removed one. No new typecheck
errors are introduced by this slice.

**Vitest baseline.** Before: filters.test.ts (15 tests) +
route.test.ts (4 tests) = 19 tests. After: filters.test.ts (16
tests; +1 scope assertion in two existing describe blocks) +
route.test.ts (7 tests; +3 scope-forwarding cases) = 23 tests. All
green.

**Go unit-test baseline.** Before: 31 tests in
`internal/api/anchors/`. After: 32 tests (+1 `TestScopeCellHelper`
table-driven across 4 cases). All green.

**Confidence:** high.

---

## Closing note on JUDGMENT-vs-runtime boundary

This slice ships a frontend filter UI and a server-side query
extension. It does NOT touch any board narrative, audit-binding
artifact, or AI-assist surface. The CLAUDE.md "AI-assist boundary
(hard)" stays untouched. The "JUDGMENT" type applies to the build-
time UX calls captured above (D1, D3, D4, D5) — the product runtime
behavior is otherwise unaffected.
