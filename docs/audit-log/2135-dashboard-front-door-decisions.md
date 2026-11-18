# #2135 — Dashboard redesign: needs attention first

Ticket: [#2135](https://github.com/mgoodric/security-atlas/issues/2135)
(parent [#2072](https://github.com/mgoodric/security-atlas/issues/2072)).
Builds on #2134 (`GET /v1/needs-attention`), #2475/#2528 (the register's
residual ranking), #2476 (module-filtered calendar and activity) and
#2542 (audit periods).

detection_tier_actual: none
detection_tier_target: none

## D1. The type filter is the server's, and the response counts types

The full page filters by type. Filtering in the browser would break
paging and totals, so `GET /v1/needs-attention` takes `?type=`
(comma-separated, validated against `needsattention.ItemTypes`; unknown
is 400). The page, `total` and band `counts` then cover those types;
a new `type_counts` counts every type in the view before the filter, so
each filter chip shows what it holds.

The dashboard's "type chips" are the per-row type labels; the type
filters live on the full page, as the parent's implementation decision
reads ("The full needs-attention page uses the same endpoint, with type
filters").

## D2. Everyone shows only when `/v1/me` says `is_approver`

`for=all` is gated by `authz.IsTenantApprover`; `/v1/me`'s `is_approver`
is that same function. The toggle reads it; the platform still refuses
`for=all` to anyone else (403), so the toggle is a convenience, not the
gate. The first load omits `for`, so the server picks the view (For you,
or Everyone for a user who owns nothing and may see it) and the toggle
shows the view the response names.

## D3. Top risks use the register's query, unchanged

`GET /v1/risks?status=open&include_rollups=false&sort=-residual&limit=5`.
Every parameter already existed: `status` defaults to open, roll-ups are
excluded unless `include_rollups=true`, and `-residual` is the register's
3-tier order (5x5, then FAIR, then unscored). The explicit
`include_rollups=false` and `status=open` are there so the query reads as
the card's contract. No server change was needed. The card drops the old
`treatment=mitigate` filter: the parent asks for open risks, whatever
their treatment.

## D4. Recent activity reads the unified log, with a server-side limit

The panel used to read `GET /v1/activity` (the evidence-ingest archive),
which printed raw event kinds. It now reads `GET /v1/activity/unified`,
the `/activity` page's source, which the platform filters by module
(#2477), and renders rows through `describeEvent` (#2093). That endpoint
returned a fixed page of up to 1000 rows, so it gains `?limit=` (1 to
1000); the dashboard asks for 10. The window is the endpoint's widest
(90 days), so a quiet tenant still shows its last changes.

Module filtering runs after the page is cut, so a member with modules
hidden can see fewer than 10 rows. That matches the `/activity` page and
is not new here.

`GET /v1/activity` and `/api/dashboard/activity` are no longer read by
the dashboard. They stay: the activity contract test pins the route's
golden against the Go producer, and removing an endpoint is its own
change.

## D5. The audit banner counts calendar days in the viewer's timezone

`period_end` is a date with no time. "Today" is the browser's local date,
and the count is whole calendar days between them (both as UTC midnights,
so a DST change cannot shift it by one). The count therefore turns over at
the viewer's midnight. The alternative, the user's saved `time_zone`, is
null for most users and would show a different day from the one their
clock shows. Copy: "Audit ends in N days", "in 1 day", "today", and
"ended N days ago" for an open period past its end date. The banner shows
only while a period is `open` (frozen periods are excluded, as before).

## D6. The older panels stay, after the four sections

Evidence freshness, recent drift and the portfolio summary are not in the
ticket's list, and the parent puts new widgets out of scope but does not
remove these. They follow the four sections. Freshness keeps its
`#evidence-freshness` anchor, which the staleness digest links to.

## D7. Playwright mocks the BFF; the Go suite pins the server

The e2e spec mocks `/api/needs-attention`, `/api/me`, the risks BFF,
`/api/audits` and `/api/activity` with `page.route` (the dashboard.spec
pattern, with `e2e_no_prefetch`). The two-user, two-tenant fixture with
every needs-attention source already exists as `internal/dbtest/attention`
(#2130/#2134); `needs_attention_2134_integration_test.go` runs For you,
Everyone, ordering, paging, tenant isolation, module narrowing and (new
here) the type filter against real Postgres with RLS. Seeding the same
fixture into the e2e database would duplicate it for a browser check that
the page asks for the right view and renders what it gets.

## D8. One failing section leaves the rest

Each section keeps its own TanStack query and `PanelCard` error state with
Retry. The spec fails Needs attention and checks the other sections
render, then fails the other four sections' endpoints and checks Needs
attention still renders and each failed section shows its own error.
