# #2130 — the calendar is the one list of what is due

Two lists covered due dates, and they disagreed: the dashboard's
"Upcoming" panel read the slice-066 rollup (exceptions, policy
acknowledgements, vendor reviews and renewals, audit periods), and the
compliance calendar read a different set (audits, exceptions, policy
reviews, vendors, periodic control reviews, offboarding). Neither had risk
reviews or action-plan due dates. The ticket makes the calendar the single
source: it gains risk reviews and action-plan due dates with overdue and
due-soon status, the dashboard's "Coming up" reads it for the next 14
days, and the rollup endpoint is removed. The build-time calls:

detection_tier_actual: none
detection_tier_target: none

## D1 — Status for the new types comes from one Go rule

The estate's overdue rule is "overdue strictly after the UTC due day"
(#2009 / #2075 for policy reviews, #2059 for risk reviews, #2184 for
vendors). The rule lived in `internal/policy` as `ReviewStatusOf` with a
private `utcDay`. It moves to a new `internal/duedate` package as
`Status(due, now, soonDays)`; `policy.ReviewStatusOf` now calls it with its
30 days, so the policy list is unchanged.

The two new SQL branches return an empty status, and
`calendar.Store.ListEvents` fills it with `duedate.Status(starts_at, now,
14)`. That keeps the comparison in one place instead of a third SQL copy of
it. The existing branches keep their own SQL status (policy reviews already
implement the same rule in SQL; control, offboarding and vendor statuses are
untouched): changing them is outside this ticket.

Due-soon is 14 days, the window the control-review branch already uses and
the dashboard's "Coming up" shows.

## D2 — Roll-up risks get review events; nothing is listed twice

#2182 leaves roll-ups out of risk lists and counts so a pattern is not
counted alongside the children it summarises. A review date is different:
#2059 gives every risk, roll-up or not, its own `review_due_at`, and a
roll-up's review is a real task on its own date. So every risk with a review
date gets one event. The branch reads `risks` only (no join to
`risk_aggregations`), so a child under one or several roll-ups is still one
row. `TestCalendar_RollUpAndChildrenEachListedOnce` pins this with two
roll-ups over the same two children.

Risks have no closed state yet (#2107), so no risk is left out on status.

## D3 — Which action plans are on the calendar

Open plans only: not `completed`, not `verified`, not tombstoned, and with a
`due_date`. A completed plan has nothing left due. `due_date` is a DATE; the
event is placed at 00:00 UTC on that day
(`due_date::timestamp AT TIME ZONE 'UTC'`) so the day does not move with the
session time zone, and the UTC-day rule in D1 reads it as that day.

## D4 — Past-due items

Every branch already filters its own date against `[from, to)`, so a window
whose `from` is in the past returns what fell due in it. No new parameter.
The default window (no `from`) still starts now, so past-due items appear
only when asked for. Needs-attention (#2134) will ask for them.

## D5 — Where each `/v1/upcoming` caller went

| Caller                                                           | Now                                                                                                 |
| ---------------------------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| Dashboard "Upcoming" panel (`upcoming-panel.tsx`, `page.tsx`)    | "Coming up", `fetchDashboardComingUp` → `/api/calendar?from=today&to=today+13` (inclusive, 14 days) |
| Dashboard SSR prefetch (`dashboard-prefetch.ts`)                 | `getComingUp` → `GET /v1/calendar`, key `["dashboard","coming-up"]`                                 |
| BFF `web/app/api/dashboard/upcoming`                             | deleted; the calendar BFF already existed                                                           |
| Dashboard export (`dashboardexport` `upcoming` panel)            | `calendar.Store.ListEvents` over today + 14 days                                                    |
| OPA `upcoming` admits (viewer, control_owner, auditor)           | removed; `calendar` was already admitted for each role, and the slice-156 matrix now pins that      |
| Contract golden + consumer test, `fulfill-from-golden` entry     | deleted (the endpoint has no provider any more)                                                     |
| Route goldens, `openapi/routes.go`, `docs/openapi.yaml`          | entry removed                                                                                       |
| `ListUpcomingItems` SQL + handler, store, cursor sentinel, tests | removed                                                                                             |

No MCP tool, CLI command, SDK or docs-site page called it.

## D6 — The export keeps its `upcoming` panel columns

The dashboard export's `upcoming` panel (JSON key, CSV file, XLSX sheet)
keeps its name and its five columns, `due_date, category, title,
resource_type, resource_id`, now filled from calendar events
(`starts_at`, `type`, `title`, `related_entity_kind`, `related_entity_id`).
Renaming them would break anyone parsing the export for no gain here. The
rows themselves change: categories are calendar types (`exception`,
`policy`, `risk`, ...), not the rollup's (`policy_ack`, `vendor_review`), and
policy acknowledgement expiries are no longer listed, because the calendar
does not carry them. That is the point of one source.

## D7 — The front-door fixture lives beside the calendar tests

The parent spec (#2072) asks for one fixture: two tenants, two users each,
and every needs-attention source. It is
`internal/api/calendar/attention_fixture_integration_test.go`. The
calendar reads its dated part; the rest (failing control, pending approval,
unowned control and risk) is seeded so tenant isolation is checked against
the whole picture. #2119's approvals query has not landed, so the pending
approval is an exception in `requested`. When #2134 builds needs attention,
it can lift the fixture into a shared package; doing that now, with one
consumer, would be a guess at its shape.

The e2e dashboard fixture moves its exception from 14 to 10 days (14 days
at `now()` sits past a window that ends at midnight) and adds a risk review
due in 7 days and an action plan due in 5.
