# 067 — Risk-hierarchy backend read endpoints — decisions log

Slice 067 is `Type: AFK` — its acceptance criteria are mechanically
verifiable. But, like slices 064 and 066 before it (the analogous
"backend slice filling a frontend slice's placeholders"), it surfaced
genuine build-time wire-shape judgment calls: the issue's AC text and
slice 056's decisions-log gap inventory disagree on a few names and
shapes, and a few semantics (constraint-filter logic, severity bucketing)
were left to the implementer. This log records the calls in the
JUDGMENT-slice format (Decisions made · Revisit once in use · Confidence
per decision) so the maintainer can re-evaluate them once slice 056's
frontend is re-pointed at these endpoints. It does NOT block merge.

The grill-with-docs gate (run before implementation, against
`Plans/canvas/06-risk.md` §6.4–6.7 and `docs/audit-log/056-...-decisions.md`)
surfaced every item below; none is a constitutional-invariant conflict.

## Decisions made

### 1. Heatmap cell theme identity is the theme SLUG, not a UUID

**The drift:** issue AC-3 specifies the heatmap cell shape as
`{theme_id, org_unit_id, risk_count, aggregate_severity}` — "theme_id"
implies a UUID. But `risks.themes` is a `TEXT[]` of theme-name slugs
(`ownership`, `tech-debt`, ...), `GET /v1/themes` (slice 053) returns
themes keyed by `name` with **no `id` field**, and slice 056's heatmap
columns are therefore keyed by theme name. AC-7 of this very issue says
"slice 056's merged PR + its decisions log are the spec."

**Chosen:** the cell field is `theme` (the slug string), not `theme_id`.
Using a UUID would force slice 056's frontend into a second lookup
(`GET /v1/themes` returns no id to join on) and contradict the contract
slice 056 actually shipped. `org_unit_id` stays a real UUID — `org_units`
_does_ have a UUID `id` and slice 056's org tree is keyed on it.
**Confidence: high** — the issue's own AC-7 makes slice 056's merged
contract authoritative over AC-3's loose wording.

### 2. No `meta_risk_present` field on the heatmap cell

Slice 056's decisions log (#2, and the "endpoint gap inventory" table's
"Heatmap meta-risk icon" row) _wishes_ for a `meta_risk_present` flag on
each cell — a marker for cells where an aggregation rule has fired. But
the issue's AC-3 defines the cell shape as exactly four fields and does
NOT list it, and the constitutional-invariants section only requires that
the heatmap "counts rule-driven meta-risks and manual aggregations as
peers" — i.e. must not _filter either out_.

**Chosen:** the aggregation counts every risk attributed to a
(theme, org_unit) cell — meta-risk or leaf, manual or rule-driven —
without distinction, satisfying invariant 9. No `meta_risk_present` field
is added: it is not in AC-3, slice 056's own decisions log explicitly
defers the meta-risk icon "with the cell counts" as a downstream concern,
and adding an un-speced field is the scope creep the per-slice template
warns against. **Confidence: high** — the invariant is satisfied (peers,
not filtered); the icon is a documented slice-056-follow-up, not a 067
deliverable.

### 3. `risk_counts` map is keyed by the raw severity scalar (0–25)

Issue AC-1 says `risk_counts` is "a map of severity → count". Severity is
the 5×5-grid scalar `likelihood × impact`, range 0–25. There is no
named-severity-band vocabulary anywhere in the codebase (the slice-019
risk heatmap works in raw `(likelihood, impact)` cells; `internal/risk/
severity.go` works in the raw 0–25 scalar). Slice 056's decisions log
mentions "color-coded chips", which implies the frontend bands the values
for display.

**Chosen:** `risk_counts` is a `map[string]int` keyed by the severity
scalar as a decimal string (`{"20": 2, "6": 1}`), sparse (a severity with
zero risks has no key). The frontend owns the 5-step color/band mapping
(it already owns the heatmap's 5-step color per slice 056 decision #1).
Inventing a band enum here — with no canvas or codebase precedent — would
be exactly the un-speced design the constitutional anti-patterns reject.
A risk whose `inherent_score` carries no numeric severity component lands
under key `"0"` — counted, never hidden (invariant 9 spirit; same
graceful-degradation posture as slice 066's residual-magnitude sort).
**Confidence: medium-high** — literal reading of the AC; revisit if the
frontend would rather receive pre-banded buckets.

### 4. Severity is COMPUTED, not selected — `risks` has no severity column

AC-2 adds `severity` to `riskWire`, but `risks` has no `severity` column.
Severity is `likelihood × impact` on the 5×5 grid (canvas §6.6); slice 053
additionally stamps an explicit numeric `severity` field inside the
`inherent_score` JSONB for aggregated parent risks.

**Chosen:** `riskWire.severity` (and the heatmap's `aggregate_severity`,
and the org-unit `risk_counts` keys) are all computed: the explicit
`inherent_score.severity` field when present (aggregated parent risks),
else `likelihood × impact`, else `0` for a non-5×5 / malformed score. The
SQL extracts the raw JSONB components guarded by `jsonb_typeof(...) =
'number'` (the exact slice-019 `HeatmapBuckets` pattern — pgx cannot cast
a non-numeric jsonb value to int); the Go layer does the multiply. This
keeps the SQL free of fragile cast chains and matches the existing
`residualMagnitude` precedent. **Confidence: high** — direct extension of
established codebase patterns.

### 5. `?constraints=` filter is OR-within-facet (array intersection)

AC-5 says `?constraints=<csv>` is "multi-value" but does not specify AND
vs OR semantics for a decision whose `constraints` array is matched
against multiple requested tags.

**Chosen:** OR-within-facet — a decision matches if its `constraints`
array intersects the requested set (`?constraints=time-pressure,cost`
matches a decision tagged with _either_). This is the conventional
faceted-filter-bar behavior (selecting two tags in one facet broadens,
not narrows) and matches how slice 056's URL-deep-linkable filter bar
would behave. Across _different_ filters (`constraints` vs
`decision_maker` vs the revisit range) the composition is AND, like every
other filter. **Confidence: high** — standard faceted-filtering
convention; the alternative (AND-within-facet) would make a multi-select
constraint facet nearly always return empty.

### 6. `?revisit_by_from`/`?revisit_by_to` is status-agnostic

Slice 055's existing `?revisit_due_within_days=N` is an `active`-only,
"due soon" dashboard cut. Slice 067 adds a `revisit_by` date _range_.

**Chosen:** the new range filter is status-agnostic — it filters purely
on the `revisit_by` date, so slice 056's filter bar can find a
`revisited` decision by its revisit date. A decision with a NULL
`revisit_by` is excluded when either bound is set (there is no date to
fall in the window — consistent with `revisit_due_within_days`'
`revisit_by IS NOT NULL` guard). The two revisit filters compose by
intersection if both are supplied. **Confidence: medium-high** — the
filter bar is a find-anything tool, not a dashboard "due soon" widget, so
status-agnostic is the right default; revisit if the filter bar is
expected to mirror `revisit_due_within_days`' active-only scoping.

### 7. Handler-level program-read authz guard, touched read endpoints only

`internal/api/risks`, `internal/api/decisions`, and `internal/api/orgunits`
had **no handler-level authz guard** on `main` — they relied solely on the
slice-035 OPA middleware, which is `nil` in `api.New(api.Config{})` test
servers. AC-6 + AC-8 require a testable 403 for an unauthorized role.

**Chosen:** add a handler-level `requireProgramRead` guard — copied
verbatim from slices 064 (`controldetail`) and 066 (`dashboard`), same
role set (admin wildcard / `IsApprover` / non-empty `OwnerRoles`) — but
apply it ONLY to the read endpoints slice 067 touches:
`orgunits.List`, `risks.ListRisks`, the new `risks.ThemeHeatmap`, and
`decisions.ListDecisions`. It is **not** retrofitted onto the
slice-019/020/053/055 write/read-one endpoints — retrofitting authz onto
untouched endpoints is out of slice-067 scope and would risk other
slices' tests. The existing `risks`/`decisions` integration tests issue
_owner_ credentials (`OwnerRoles` non-empty → guard admits them), so they
stay green — confirmed by re-running slice 066's sort tests. The slice-056
hierarchical risk dashboard audience (CISO / program-lead) is exactly the
operator/auditor audience the dashboard slice's guard already serves, so
reusing the identical derivation keeps the backend-for-frontend slices
coherent. **Confidence: high** — direct copy of the 064/066 precedent.

## Revisit once in use

- **`risk_counts` shape** (decision #3) — if slice 056's frontend would
  rather receive pre-banded severity buckets (critical/high/medium/low)
  than the raw 0–25 scalar map, change the org-units handler to band
  server-side. The org-unit aggregation query does not change — only the
  Go-side map keying.
- **`meta_risk_present`** (decision #2) — if slice 056's heatmap
  meta-risk icon is prioritised, add a `meta_risk_present` boolean to the
  heatmap cell. It needs a `LEFT JOIN risk_aggregations` (or an
  `inherent_score ? 'aggregation_key'` test) folded into the
  `RiskThemeOrgUnitGrid` query — additive, no shape break.
- **revisit-range status scoping** (decision #6) — if the filter bar is
  expected to mirror `revisit_due_within_days`' `active`-only scoping,
  add an `active`-only guard to the `RevisitByFrom`/`RevisitByTo` branch
  of `decision.matchesRicherFilters`.
- **authz role source** (decision #7) — when slice 035's DB-backed
  `user_roles` becomes the role source of truth, the three new
  `requireProgramRead` guards should re-derive from the resolved role set
  rather than the credential flags — the same revisit slices 064/066
  record.
- **in-memory filtering at scale** — `risk.Store.List` and
  `decision.Store.List` filter in memory (the established slice-019/055
  pattern; v1 cardinality is small per the anti-criteria budget — 500
  risks / 50 decisions). If a tenant's register grows large, the
  `?theme=`/`?org_unit=`/`?constraints=` predicates should move into the
  sqlc query layer. The `idx_risks_themes_gin` and
  `idx_risks_tenant_org_unit` indexes (slice 052) already exist to back
  that move.

## Confidence summary

| Decision                                              | Confidence  |
| ----------------------------------------------------- | ----------- |
| 1. Heatmap theme key is the slug, not a UUID          | high        |
| 2. No `meta_risk_present` field (peers, not filtered) | high        |
| 3. `risk_counts` keyed by raw severity scalar         | medium-high |
| 4. Severity computed, not selected                    | high        |
| 5. `?constraints=` is OR-within-facet                 | high        |
| 6. `?revisit_by` range is status-agnostic             | medium-high |
| 7. Program-read guard on touched read endpoints only  | high        |
