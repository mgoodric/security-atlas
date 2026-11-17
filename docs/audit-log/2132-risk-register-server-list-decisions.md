# Issue #2132 — The risk register is filtered, sorted and paged on the server

`/risks` used to load every risk and filter, sort and page it in the
browser. `GET /v1/risks` now takes the filters and an allow-listed sort,
pages in SQL (`ListRisksPage`), and counts with the same filters
(`CountRisksPage`). The page asks for the one page it shows. The shape
follows the controls list (#2131).

- `detection_tier_actual`: playwright
- `detection_tier_target`: playwright

The bug found on the way (D8) was the default columns overflowing 1316px
by 188px, caught by the new Playwright width check before the PR.

## D1 — One allow-listed sort parameter

**Decision.** `?sort=key` sorts ascending and `?sort=-key` descending. The
key is one of `id`, `title`, `category`, `owner`, `residual`, `treatment`,
`review_due`, `status`, `methodology`, `level`, `org_unit`, `themes` and
`created_at`: one per column the register can show. The dashboard's
`residual,age` is kept as an alias. `risk.ParsePageSort` refuses anything
else with a 400. In SQL each key is a fixed `CASE WHEN sort_key = '…'`
branch; nothing is interpolated. Every order ends on `r.id`, so offset
paging returns each row once. Text keys sort `lower(x) COLLATE "C"`.

**Why.** The same reasons as #2131 D1 to D3. The URL `sort` is the platform
parameter verbatim, so a bookmark, a saved view (#2133) and the request say
the same thing. An old `severity:asc` bookmark reads as the default.

## D2 — Residual sorts 5x5 above FAIR, in both directions

**Decision.** The residual sort orders by group first, every non-FAIR
(5x5) risk above every FAIR risk, whichever direction is asked; then by
the residual value in the asked direction; a risk with no residual value
sorts last within its group in both directions. The value is #2185's
`risk.ScoreValue`: `score`, then `severity`, then `likelihood × impact`.

**Why.** A FAIR residual is dollars and a 5x5 residual is 1..25, so one
ordered list of both is meaningless (dispatcher's brief, and #2185's
`RanksAbove`). Putting FAIR last in both directions, rather than flipping
the groups on ascending, keeps the 1..25 scale at the top of the register
where the bands mean something. The integration test
`TestListRisksPage_ResidualOrderMatchesRanksAbove` sorts the same rows in
Go with `risk.RanksAbove` and `risk.ScoreValue` (#2185) and checks the SQL
returned that order on a mixed 5x5, FAIR and unscored fixture.

The dashboard's `residual,age` gets the same grouping. Before, its in-Go
sort ranked a FAIR dollar figure against 5x5 scores.

## D3 — The default status is open; `status` replaces `include_closed`

**Decision.** `?status=open|closed|all`, default open, as the register has
left closed risks out since #2107. `include_closed=true` still means all,
for the older callers. On the page, a Status filter replaces the "Show
closed" checkbox.

**Why.** The ticket names status as a filter. A filter with three values
is one control where a checkbox could only add closed risks to open ones,
never show closed risks alone.

## D4 — Roll-ups follow the open-children rule, in SQL

**Decision.** A roll-up is a risk with at least one open child (#2107's
rule, already in `ListRiskChildCountsForTenant`). The list and the count
both compute the child count in a lateral subquery and leave roll-ups out
unless `include_rollups=true`.

**Why.** The rule must hold before paging, or a page could come back short
and the total would count rows the list never shows.

## D5 — Owner, review due and paging values

**Decision.**

- `?owner=` takes a user id (#2129's `owner_user_id`) or `none`, risks with
  no owner user. Free text kept as "Owner (unlinked)" counts as unowned.
- `?review_due=overdue|due_soon` and `?review_due_from` / `?review_due_to`
  (inclusive UTC days) fold into one half-open `[from, before)` range on
  `review_due_at`. Overdue and due soon use the calendar's rule
  (`duedate`): overdue once the UTC due day has ended, due soon within 14
  days. All three compose.
- `limit` defaults to 500 and is capped at 500; `offset` defaults to 0.

**Why.** "Unowned" is one of the needs-attention sources the front-door
fixture seeds, and an unlinked owner is not a person who can act. The
calendar is the one "what's due" (#2072), so the register uses its rule.
A default page of 500 keeps the pickers and the sidebar badge, which read
the whole list, working at v1 tenant sizes without a walk.

## D6 — Default columns; inherent moves to hover

**Decision.** The default columns are ID (the first eight hex digits of the
id, full id on hover), Title, Category, Owner, Residual, Treatment, Review
due and Status. Residual shows the band; hovering it shows the inherent
severity. The Inherent severity column, the Severity filter and the per-row
"View in hierarchy" link are gone. The chooser adds methodology, level,
org unit, themes and created, kept in `?cols=`.

**Why.** The ticket fixes the default set and asks for 1316px with no
sideways scroll. The severity filter was a client-side band filter with no
server counterpart in the ticket. The hierarchy view stays one click away
in the page header.

#2083's enum-labels spec asserted "no hash-prefix ID column". That change
removed hash prefixes under titles; it did not decide against an ID column,
which #2132 asks for. The assertion now checks the title cell has no
prefix.

## D7 — The "N of M" count

**Decision.** When a filter or toggle narrows the register, the count reads
"N of M risks", where M is the sidebar badge's read (open risks, no
roll-ups) shared under its query key. When N is larger than M (closed or
roll-ups shown), it reads "N risks".

**Why.** The page holds one page of rows, so it cannot count the register
itself. A second request with no filters would repeat the badge's.

## D8 — Text columns wrap

**Decision.** Title, Category and Owner cells are `whitespace-normal`; the
title has a 14rem floor. The other columns stay `whitespace-nowrap`.

**Why.** shadcn table cells never wrap, so one long title or unlinked owner
pushed the table 188px past its container at 1316px. The Playwright check
caught it.

## D9 — The front-door fixture names risk owners as users

**Decision.** `internal/dbtest/attention` now sets `owner_user_id` on its
owned risks as well as the free-text owner. The unowned risk has neither.

**Why.** Since #2129 a risk's owner is a user; the fixture predated that,
so "unowned" and "owned by A" could not be told apart by the owner filter.
The calendar and controls tests that also seed it do not read the risk
owner.

## D10 — Paging is offset, not cursor

**Decision.** `limit` / `offset`, as the controls list (#2131).

**Why.** The register is small at v1 sizes, the total id tiebreaker makes
offset paging stable, and a cursor would need a per-sort-key encoding.
