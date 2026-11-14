# #2067 — global search: policies, grouped by type, own records first

`GET /v1/search` covered anchors, controls, risks and evidence, merged by
relevance with anchors first on ties, so a common word listed SCF catalog
anchors above the organisation's own controls. Policies were not searched,
and the popover showed no counts. Triage fixed the shape: search policies;
group results in the order controls, risks, policies, evidence, anchors;
best match first within a group; a count and "show all" per group. The
build-time calls:

detection_tier_actual: none
detection_tier_target: none

## D1 — Policy hits follow the policies read admit, nothing else

The handler already runs one OPA decision per type. Policies use
`resource.type = "policies"`, the rule `GET /v1/policies` passes. That
list has no draft filter: it returns every status to every role it admits.
Search therefore shows drafts to exactly the callers who can already list
them, and adds no visibility rule of its own. A unit test runs every role
through both decisions and requires them to agree. If drafts should ever be
hidden from some role, that rule belongs on the policies read path, and
search will need the same predicate.

## D2 — Superseded versions are left out; a non-published row says what it is

Each policy version is its own row. Searching every row would list a
three-version policy three times. Superseded rows are excluded, matching how
controls exclude `superseded_by IS NOT NULL`. A published policy with a draft
next version still yields two hits, so a non-published hit's title carries
`· v<version> <status>` to tell them apart.

## D3 — The `limit` is shared across groups, one row per group per round

With hits sorted by group, truncating the merged list to `limit` would let
a group with many matches push every later group out. The merge takes one
row from each group in turn until `limit` is reached, then emits the rows
in group order. Each group's full count rides in the new `groups` array, so
the UI knows when a group was cut short.

## D4 — Counts come from `count(*) OVER ()` in the same query

No second COUNT query per type. Each per-type query projects the window
count, which Postgres computes before LIMIT. Tenant types stay inside the
RLS-bound transaction, so a count never includes another tenant's rows
(covered by an integration test).

## D5 — The per-type SQL ranks by matched tokens before LIMIT

The queries used to order by title and cut at 25, so "best match first"
could only reorder whatever the alphabet let through. They now order by the
number of query tokens matched, title as tiebreaker, and the per-type cap is
the request's `limit`. The old fixed `PerTypeTopK` constant is gone.

## D6 — "Show all" refetches one type inside the popover

The list pages have no free-text filter to link to. "Show all" calls the
same endpoint with `types=<type>&limit=50` and renders that group's rows in
place. Groups above 50 matches show the first 50; the count still says how
many exist.

## D7 — The Playwright spec for counts and show-all is route-mocked

Seeding a real policy into the shared e2e tenant would break
`policy-review.spec.ts`, which asserts that tenant's exact policy count, and
e2e fixtures are additive within a CI run. The new spec mocks `/api/search`
as `controls-top-bar.spec.ts` does. The real-endpoint spec
(`global-search.spec.ts`) asserts the `groups` order and the rendered counts
for its seeded control, risk and evidence. Policy search against real data
is covered by the Go integration tests.
