# #2232 — one overdue rule, and role-gated vendor and exception events

Follow-ups from the review of #2230 (#2130). There were two problems:

1. Vendor reviews still had their own overdue copies: a Go flag that
   compared a timestamp, two SQL predicates, and a calendar branch that
   computed no overdue status at all.
2. Every calendar reader saw vendor and exception events, including roles
   that cannot read vendors or exceptions.

detection_tier_actual: manual_review
detection_tier_target: integration

## D1 — The vendor due date is one SQL function, mirrored in Go

`vendor_review_due_date(last_review_date, review_cadence, created_at,
initial_review_days) RETURNS date` (migration `20261122000000`) is the only
definition of the date. It returns `last_review_date` plus the cadence as a
calendar interval (1/3/6/12 months), or, for a vendor never reviewed, the
UTC date of `created_at` plus the tenant's initial-review window (#2061).
`ListOverdueVendors`, `CountVendorsForBurndown` and the calendar's
vendor-review branch all call it. Overdue is `due < cutoff::date`, which is
the `internal/duedate` rule: overdue strictly after the UTC due day.

`vendor.Vendor.ReviewDueDate()` is the Go mirror. It adds calendar months
and clamps the day to the end of the month, as Postgres `date + interval`
does. `IsOverdueAsOf` is now `duedate.IsOverdue(ReviewDueDate(), cutoff)`.
`ReviewCadence.Duration()` (30/91/182/365 days) is replaced by `Months()`,
and its only caller was `IsOverdueAsOf`.

The two disagree on an unknown cadence. `Months()` treats it as 12 months,
so it fails closed and the vendor eventually shows as overdue. The SQL
`CASE` has no `ELSE`, so it returns NULL, and a NULL due date is never
overdue. That case can't happen: `review_cadence` is the
`vendor_review_cadence` enum, and the `CASE` covers every value of it. A
new enum value needs a matching arm in the function and in `Months()`.

A SQL function rather than Go-only status, because the list and the
burndown filter and count in SQL. Computing the date in two languages is
how the copies drifted in the first place.

## D2 — Who changes status

The SQL list and burndown already compared at day granularity:
`date + interval` is a `timestamp` at 00:00, and `< cutoff::date` compares
it to midnight of the cutoff day. Calendar months were already used there,
so **no vendor's membership in the overdue list or the burndown count
changes**.

The Go flag changes. It drives the vendor API's `overdue` field
(`api/vendors`) and the admin vendor export column. A reviewed vendor now
flips in two cases:

- **On its due day.** It was overdue from 00:00 UTC on the due day; it is
  now overdue only from the next UTC day. This is the timestamp-vs-day bug
  the ticket names.
- **Cadence day counts.** The old 30/91/182/365-day approximations put the
  due date up to about three days off the calendar-month date SQL uses.
  For example, Feb 10 + monthly was Mar 12, not Mar 10, and Dec 10 +
  quarterly was Mar 11, not Mar 10. Around those dates the Go flag
  disagreed with the overdue list. Now it agrees.

Never-reviewed vendors do not change: their branch already used the UTC
day.

The parity test (`vendor_overdue_parity_integration_test.go`) seeds a
monthly, a quarterly, an annual and a never-reviewed vendor, all due on
2026-03-10. At 23:59:59.999 UTC that day, none of the four is overdue in
the list, the burndown, the calendar or the Go flag. At 00:00 UTC the next
day, all four are overdue everywhere. Under the old Go flag, the monthly
vendor (Mar 12) and the quarterly vendor (Mar 11) were still not overdue at
the second instant.

## D3 — Calendar vendor-review events change shape

- `status` was the cadence string (`monthly`, …), a value no other event
  type uses. It is now `overdue` / `due-soon` / `upcoming` from
  `duedate.Status` with the calendar's 14-day window. The cadence stays in
  `cadence`. The web agenda only reads `status` for control events, so
  nothing in the UI changes.
- Never-reviewed vendors now get a review event on their first-review due
  date. They used to be left out ("no anchor to project from"), so a vendor
  could be on the overdue list and absent from the calendar.
- Vendor renewal rows keep their commercial status. The store fills status
  only for rows whose SQL status is empty.

## D4 — Policy review status moves from SQL to the store

The calendar's policy branch had its own SQL copy of the day rule with a
30-day window. It now returns an empty status, and the store fills it with
`duedate.Status(next_review_at, now, policy.ReviewDueSoonDays)`, the call
the policy list makes. The values are unchanged.

## D5 — Vendor and exception events follow the list endpoints' OPA reads

`gatedEventTypes` gains `vendor -> vendors` and `exception -> exceptions`,
the resource types `GET /v1/vendors` and `GET /v1/exceptions` resolve to.
With the current rego that gives:

| Role          | vendor | exception |
| ------------- | ------ | --------- |
| admin         | yes    | yes       |
| grc_engineer  | yes    | yes       |
| viewer        | yes    | **no**    |
| auditor       | **no** | yes       |
| control_owner | **no** | **no**    |

The ticket names the control owner and the auditor. The viewer also loses
exception events, because the viewer cannot list exceptions either. No new
matrix was written: OPA decides.

## D6 — The ICS feed fails closed on these types (agreed with the dispatcher)

A calendar ICS token is minted with no user (`creds.Issue(tenant, "", …)`),
so there is no role to ask OPA about. As #2130 did for risk, action-plan
and offboarding events, the feed now leaves vendor and exception events out
for every subscriber.

**Admin ICS feeds lose vendor-review, vendor-renewal and exception-expiry
events until minter-role ICS filtering lands.** That follow-up means
resolving the role of the user who minted the token at read time. It would
turn `MintedBy` from attribution into authorization, which is Matt's call.
The dispatcher filed it as a needs-triage ticket.
