# Issue #2131 — The controls list is filtered, sorted and paged on the server

`/controls` used to load every control (a walk of 500-row pages) and filter
it in the browser. `GET /v1/controls?include=state` now takes the filters
and an allow-listed sort, pages in SQL, and counts with the same filters.
The page asks for the one page it shows.

- `detection_tier_actual`: integration
- `detection_tier_target`: integration

The bug found on the way (D6) surfaced while writing the server's value
allow-list, which the integration suite pins.

## D1 — One allow-listed sort parameter

**Decision.** `?sort=key` sorts ascending and `?sort=-key` descending. The
key is one of `scf_id`, `name`, `family`, `state`, `freshness`,
`last_observed_at`, `owner` and `frameworks`, one per column the table
shows. `control.ParseListSort` refuses anything else with a 400. In SQL each
key is a fixed `CASE WHEN sort_key = '…'` expression, so no column name or
direction is ever interpolated.

**Why.** sqlc cannot take a column as a parameter, and a parameterised
`CASE` chain is the usual way to keep the statement static. A single
`sort` parameter keeps the URL short and saves with a view as one key.

## D2 — The default order, and the tiebreaker

**Decision.** With no `sort`, rows order by worst state first (the rollup's
own rank: fail, inconclusive, excepted, pass, na, then no evaluation), then
by `last_observed_at` ascending with nulls first, so a control never
observed is the stalest. Every order ends on `scf_id`, then `id`.

**Why.** "Failing first, then stalest evidence" is the ticket's default.
Ending on `id` makes the order total, so offset paging returns each row
once. The integration test walks every order two rows at a time to prove
it.

## D3 — Text sorts are lower-cased under `COLLATE "C"`

**Decision.** `name`, `family`, `scf_id` and `owner` sort on
`lower(x) COLLATE "C"`.

**Why.** The database's default collation decides how spaces and
punctuation compare, and differs between deployments. `C` gives the same
order everywhere and the same order as a byte comparison in tests.
Lower-casing keeps "alpha" and "Beta" in the order a person expects. The
cost is that accented letters sort by code point, which is acceptable for
control titles.

## D4 — The count takes every filter

**Decision.** `CountActiveControls` repeats the list's rollup CTEs and
`WHERE` clause, so `total` is the size of the filtered set the footer pages
over.

**Why.** The page needs the filtered total for "N of M" and the pager, and
a window `count(*) OVER ()` returns nothing on a page past the end. The
unfiltered count comes from the same call with no filters: the sidebar
badge already makes it, under the same query key.

## D5 — Owner filter values, and the families list

**Decision.** `?owner=` takes a user id (#2128) or `none` for controls
nobody is assigned. The response carries `families`, every family of the
tenant's active controls, unfiltered.

**Why.** "Unowned" is one of the needs-attention sources the front-door
fixture seeds, and the owner pill offers it. The Family pill used to list
the families of the rows in hand; with one page in hand it would list
only that page's, so the server sends the set.

## D6 — The State and Freshness options were values the rollup never produces

**Decision.** The `EvaluationState` labels are now the `evidence_result`
values (`pass`, `fail`, `inconclusive`, `excepted`, `na`) and
`FreshnessState` gains `no_evidence`. A saved or bookmarked
`insufficient_evidence` reads as `inconclusive` and `not_applicable` as
`na`; any other value the server would refuse reads as "all".

**Why.** The pill offered `insufficient_evidence` and `not_applicable`,
which no evaluation carries, so choosing either always showed an empty
list. The server now refuses unknown values, so the page must send real
ones. Mapping the two old values on read keeps an old saved view meaning
what its label said. No stored row is rewritten.

## D7 — Saved views carry `owner` and `sort`; no data migration

**Decision.** The saved-view filter allow-list gains `owner` and `sort` on
both sides. Each saved key is already the server parameter of the same
name, so a view applies by becoming the URL. Stored views are not
rewritten.

**Why.** The existing keys (`framework`, `family`, `result`, `freshness`,
`scope`, `anchor_fit`) were already the parameter names. The only stored
values that change meaning are D6's two State values, mapped on read, so a
rewrite with its dry run and approval is not needed.

## D8 — The SCF catalog view filters by scope and has-control only

**Decision.** The "SCF catalog" view keeps its view, has-control and scope
pills and pages on the server through `GET /v1/anchors` (which already took
`limit`, `offset`, `scope` and `has_control`). The framework, family, state,
freshness, owner and fit pills, and the column sort, are on "My controls"
only. The catalog walk stays for `/catalog/scf`, which searches the whole
catalog in the browser and is not part of this ticket.

**Why.** The ticket removes client-side filtering from the controls page.
Anchors have no owner or fit. Their state rolls up every control on the
anchor, which is the controls view's job, so the catalog view gives up
those filters instead of keeping a browser filter for them.

## D9 — The selection outlives a page change

**Decision.** Bulk selection is no longer pruned against the rows on
screen. Select-all-in-view selects the current page.

**Why.** The page now holds one page of rows, so pruning would drop
selections on every page change. Bulk-assign re-checks every id on the
server, so a stale id is refused there.

## D10 — The front-door fixture moved to `internal/dbtest/attention`

**Decision.** The two-tenant, two-user fixture that #2130 wrote inside the
calendar tests is now the importable package `internal/dbtest/attention`.
The calendar tests and the controls list tests both seed it.

**Why.** The parent spec (#2072) names one fixture for every front-door
ticket. A `_test.go` file cannot be imported from another package.
