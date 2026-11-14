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

The new SQL branches (risk, action_plan, policy_ack) return an empty
status, and `calendar.Store.ListEvents` fills it with
`duedate.Status(starts_at, now, 14)`. That keeps the comparison in one place instead of a third SQL copy of
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
rows themselves change: the window is today + 14 days, and categories are
calendar types (`exception`, `policy`, `vendor`, `risk`, `policy_ack`, ...),
not the rollup's (`vendor_review`, `vendor_renewal`, `audit_period`). The
CHANGELOG lists the new vocabulary. Policy acknowledgement expiries stay in
the panel through the calendar's `policy_ack` type (D9). The export is
admin and approver only (slice 269 D3), roles that read risks and action
plans, so it reads the calendar store without the D8 narrowing.

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

## D8 — Risk and action-plan events follow the caller's OPA access

Review finding. The calendar is admitted to every signed-in role, but
auditor and control_owner are not admitted to `risks`, and only the
admin and grc_engineer wildcards admit `action-plans`. Listing risk
reviews and plan titles on the calendar would have shown those roles
records they cannot open. So the JSON handler asks OPA, as the caller,
for a read of `risks` and of `action-plans`, the per-type admit
`/v1/search` uses, and drops the denied event types from the query
(`readableTypes`). Result: admin and grc_engineer see both, a viewer sees
risk reviews only, an auditor and a control owner see neither. With no
engine wired (unit-test servers) every type stays, as in search.

The ICS feed cannot do the same: its URL token is a credstore key with
no user, and the `minted_by` attribution column says nothing authorizes
on it. So the feed leaves risk and action-plan events out for every
token (fail closed). A follow-up could bind the token to its subscriber
and evaluate that user's roles at fetch time. Vendor and exception events
keep their current visibility; role filtering for those is a separate
follow-up.

## D9 — Policy acknowledgement expiries are a calendar type

Review finding: retiring the rollup must not drop lapsing
acknowledgements. `policy_ack` is the rollup's branch moved over: the
freshest ack per (policy version, user), due 365 days on. Its title now
names the policy (a tenant-scoped LEFT JOIN on `policies`) instead of
printing the version UUID, its related entity is the acknowledgement,
and its status comes from `duedate.Status` with 14 days. It stays visible
to every calendar reader, as it was on the dashboard rollup.

## D10 — The type filter matches exactly

The SQL matched the CSV type filter with `position(type IN filter)`, a
substring test. With `policy_ack` added, a filter of `policy_ack` would
also have switched the `policy` branch on. Every branch now tests
`type = ANY(string_to_array(filter, ','))`.
