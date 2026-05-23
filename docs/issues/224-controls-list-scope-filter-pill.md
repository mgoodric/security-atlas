# 224 — Add Scope filter pill to /controls list

**Cluster:** Quality / UI hygiene (frontend + small backend)
**Estimate:** 1d (filter UI + URL plumbing) · +0.5d if the upstream join needs scope-cell filtering
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

Surfaced during slice 204 audit fleet (controls page), captured as
follow-up per continuous-batch policy. The mockup at
`Plans/mockups/controls.html` (lines 172-180) ships five filter
pills:

1. Framework
2. Family
3. State
4. Freshness
5. **Scope** (options: `All cells`, `env=prod`, `cloud=aws`,
   `bu=platform`)

The live page (`web/app/(authed)/controls/page.tsx`,
`FILTER_KEYS` array lines 86-91) ships only the first four. The
`ControlFilters` type in `web/app/(authed)/controls/filters.ts` has
no `scope` key.

The mockup's Scope options are illustrative — the real shape on a
deployed tenant is `(BU × env × geo × cloud × data_class × product)`
per canvas §5.1. The filter pill needs to enumerate the tenant's
populated cells, not hardcode `env=prod`. Today the bootstrap-seed
deployment has minimal scope cell content; the filter would render
"All cells" + whichever cells the tenant has populated.

Scope is a load-bearing constitutional primitive (invariant #4:
"Scope is multidimensional"). Filtering controls by scope cell is
the natural way to ask "what's the SOC 2 posture in
prod-aws?" — the absence of the filter forces the user to read the
full anchor list and intersect mentally.

The slice ships:

- A `scope` filter pill on `/controls`.
- A `?scope=<cell_id>` URL param (consistent with the existing
  four pills' URL plumbing).
- A BFF / upstream extension: `GET /v1/anchors?include=state&scope=<cell_id>`
  filters returned anchors to those whose `applicability_expr`
  intersects the given scope cell.

## Threat model

| STRIDE                | Threat                                                                                                    | Mitigation                                                                                                                                                                                                      |
| --------------------- | --------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing        | Scope-cell IDs from another tenant in the URL.                                                            | AC-4: the upstream query joins `scope_cells` and `controls` under tenant RLS; an out-of-tenant `scope_id` returns zero results, not a 404 leak.                                                                 |
| **T** Tampering       | None — read path.                                                                                         | n/a                                                                                                                                                                                                             |
| **R** Repudiation     | None — read path.                                                                                         | n/a                                                                                                                                                                                                             |
| **I** Info disclosure | The dropdown enumerates the tenant's scope cells — could expose business context (BU names, geo regions). | This is the tenant's OWN context, surfaced to the tenant's OWN user. No cross-tenant exposure. The dropdown filters by RLS-scoped `scope_cells`; users see only their tenant's cells. Threat is not a real one. |
| **D** DoS             | Large scope-cell list in the dropdown.                                                                    | AC-5: dropdown caps at 50 cells; if a tenant exceeds, render a typeahead instead of a static dropdown.                                                                                                          |
| **E** EoP             | None — same authz as the existing four pills.                                                             | n/a                                                                                                                                                                                                             |

**Verdict.** `no-mitigations-needed-beyond-existing`. The Spoofing
item is already covered by RLS on `scope_cells`.

## Acceptance criteria

- **AC-1.** A fifth filter pill renders on `/controls` after the
  existing four (Framework, Family, State, Freshness), labelled
  "Scope".
- **AC-2.** Pill options are populated from the BFF response: an
  "All cells" entry plus one entry per scope cell the tenant has
  defined. Cell labels follow the cell's display name (e.g.
  `prod / aws / us-east`).
- **AC-3.** Selecting a cell sets `?scope=<cell_id>` on the URL;
  clearing returns to no `scope` param (parity with the other pills'
  URL plumbing in `page.tsx` lines 134-152).
- **AC-4.** BFF `/api/controls` accepts `?scope=<cell_id>`,
  forwards to upstream `GET /v1/anchors?include=state&scope=<cell_id>`.
  Upstream query joins the scope-cell intersection (per canvas §5.5,
  `effective_scope(control, framework) = applicability_expr ∩
framework_scope.predicate`) under RLS.
- **AC-5.** Cell dropdown caps at 50 entries; if the tenant has
  more, a typeahead-search input replaces the static dropdown
  (defer the typeahead's full UX to a follow-on slice if estimate
  exceeds 1d; for v0, cap at 50 and surface a warning).
- **AC-6.** Filter logic in `web/app/(authed)/controls/filters.ts`
  updated: `ControlFilters` gains `scope` key, `applyFilters`
  passes through to the BFF query rather than client-side
  filtering (the scope intersection is a server-side concern;
  client-side filtering would require the BFF to ship every
  anchor's `applicability_expr` to the browser, which is a leak
  surface).
- **AC-7.** Vitest unit coverage: filter URL parsing, BFF query
  string construction, cap-at-50 behavior.
- **AC-8.** Playwright e2e spec: select a scope, assert URL
  updates, assert table rows re-render to the filtered set.
- **AC-9.** Per-slice docs: `docs/audit-log/224-controls-scope-filter-decisions.md`
  capturing (D1) the 50-cell cap rationale; (D2) why filtering is
  server-side, not client-side; (D3) typeahead-deferral
  judgement; (D4) CI-delta scan results.
- **AC-10.** Pre-commit clean, DCO sign-off, Co-Authored-By trailer.

## Constitutional invariants honored

- **Invariant 4 (multidimensional scope).** The pill exposes scope
  cells as first-class filter primitives, not as a tree.
- **Invariant 5 (FrameworkScope intersection).** When BOTH the
  Framework pill AND the Scope pill are set, the upstream query
  applies the full intersection — `applicability_expr ∩
framework_scope.predicate ∩ scope_cell.predicate`.
- **Invariant 6 (tenant isolation).** Scope-cell list is RLS-scoped
  to the caller's tenant.

## Canvas references

- `Plans/canvas/05-scopes.md` §5.1–5.5 — multidimensional scope +
  FrameworkScope intersection
- `Plans/canvas/12-ui-fill-in-design-decisions.md` — filter row
  shape (if a Scope-pill design call already exists, honor it)
- `Plans/mockups/controls.html` lines 172-180 — pill mockup
- `docs/audit-log/204-page-audit-controls.md` — parent audit

## Dependencies

- **#204** (UI parity audit fleet) — parent.
- **#002** (six-primitives schema, including `scope_cells`
  - tenancy plumbing) — merged. Source of the dropdown options.
- **#021** (scope cells + `applicability_expr` evaluation) —
  merged. The upstream query uses the existing scope-evaluation
  surface.

## Anti-criteria (P0 — block merge)

- **P0-224-1.** Does NOT hardcode `env=prod`, `cloud=aws`,
  `bu=platform` — those are mockup illustration. Options come
  from the tenant's scope cells.
- **P0-224-2.** Does NOT do scope-cell intersection client-side.
  Pushing `applicability_expr` to the browser exposes business
  logic; server-side filtering keeps it inside.
- **P0-224-3.** Does NOT touch the slice 204 audit harness.
- **P0-224-4.** Does NOT commit any vendor-prefixed test fixture
  tokens; neutral `test-*` only.

## Skill mix (3-5)

1. Next.js App Router + shadcn/ui — pill UI + URL plumbing.
2. sqlc + RLS-aware Go handler — upstream scope intersection.
3. Vitest + Playwright — filter behavior coverage.
