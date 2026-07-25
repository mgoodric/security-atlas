# ADR 0015 — Calendar agenda and dashboard "Upcoming" share one upcoming-event source vocabulary

**Status:** Accepted.

**Date:** 2026-06-11

**Slice:** 675 (`fix/675-calendar-agenda-event-sources`).

**Surfaced by:** the 2026-06-10 demo-tenant UI audit, item ATLAS-034 — the
dashboard "Upcoming" widget listed audit periods + ~13 vendor reviews + 2
exceptions, but the compliance calendar agenda (all filters checked) showed
ONLY the 2 exceptions, and the calendar legend had no "Vendor reviews" entry.

**Touches:**

- `internal/db/queries/calendar.sql` (`ListCalendarEvents`)
- `internal/db/queries/dashboard.sql` (`ListUpcomingItems`)
- `internal/api/calendar/handler.go` (event-type vocabulary)
- `web/app/(authed)/calendar/*`, `web/components/calendar/*`

---

## Context

Two surfaces answer the same operator question — "what compliance work is
coming up?" — from two independently-authored SQL queries:

| Surface                     | Query                            | Sources                                                                       |
| --------------------------- | -------------------------------- | ----------------------------------------------------------------------------- |
| Dashboard "Upcoming" widget | `ListUpcomingItems` (slice 066)  | exceptions, policy acknowledgments (+365d), **vendor reviews**, audit periods |
| Compliance calendar agenda  | `ListCalendarEvents` (slice 094) | audit periods, exceptions, policy `next_review_at`, periodic control reviews  |

Because the two queries were written under different slices with no shared
contract, they drifted. The most visible drift: the calendar's `UNION ALL` had
**no vendor branch at all**, so vendor reviews — a first-class item on the
dashboard — never appeared on the calendar, and the calendar legend never
listed a vendor type. The demo tenant's near-term exceptions landed inside the
calendar's default 90-day window while the audit-period and vendor next-review
dates mostly did not, so the agenda collapsed to "exceptions only" and the
divergence read as a total failure rather than a partial one.

The two queries are **not** otherwise interchangeable, and collapsing them into
one shared query would regress both surfaces:

- The dashboard models policy review as the **policy-acknowledgment renewal**
  (`acknowledged_at + 365d`); the calendar models it as the **policy's
  `next_review_at`**. These are different domain events with different due
  dates. Neither is wrong; they answer different questions.
- The calendar surfaces **periodic control reviews** (cadence math over
  `control_evaluations`); the dashboard does not.
- The dashboard is keyset-paginated (stable under concurrent appends); the
  calendar is window-bounded `[from, to)` with a truncation probe. The wire
  shapes, ordering keys, and pagination models differ.
- The slice anti-criterion is explicit: **do NOT change what the dashboard
  shows** (the dashboard is the more-complete reference) and do NOT add event
  types beyond what the program already tracks.

So a literal "one query for both" DRY would either drop the calendar's
control-review feature or rewrite the dashboard's policy semantics — both
forbidden.

## Decision

DRY the two surfaces at the level that actually drifted — **the set of entity
types each surface sources** — without merging their query bodies:

1. **Add the missing `vendor` source to the calendar.** `ListCalendarEvents`
   gains a `vendor` `UNION ALL` branch that mirrors the dashboard's vendor
   cadence math (`last_review_date + review_cadence interval`), windowed like
   the calendar's other branches. This is the one source the dashboard had that
   the calendar structurally lacked. The cadence-to-interval `CASE` is copied
   verbatim from `ListUpcomingItems` so the two compute the same next-review
   date for the same vendor.

2. **Surface `vendor` everywhere the closed event-type vocabulary lives:** the
   handler's `validEventTypes` / `normalizeTypeFilter`, the frontend
   `CalendarEventType` union, the legend (`type-filter.tsx`, now "Vendor
   reviews"), and the agenda/month-grid color + label maps. The
   `linkFor` exhaustiveness guard (`assertNever`) forces every new union member
   to be handled at compile time — vendor events link to the real
   `/vendors/[id]` detail page.

3. **Document the shared vocabulary as a contract** (this ADR + cross-reference
   comments in both `.sql` files) so a future event-type addition to one
   surface is a deliberate, reviewed decision rather than a silent drift.

### Why not a shared materialized view / shared query layer?

A shared `upcoming_events` view was considered and rejected for v1: it would
have to reconcile the policy-event semantic difference and the
pagination-model difference before it could serve both callers, which is a
larger refactor than the bug warrants and risks regressing the dashboard
(anti-criterion). The view is a reasonable v2 consolidation once the policy
semantics are unified; this ADR records that as the revisit path, not the v1
shape.

## Consequences

- The calendar agenda now sources audit-period boundaries, exceptions, policy
  reviews, **vendor reviews**, and periodic control reviews. The legend lists
  every surfaced type. The `/v1/calendar.ics` feed inherits the vendor branch
  automatically (it renders directly off `ListCalendarEvents` rows), so the
  in-app agenda and the ICS feed stay consistent (AC-4).
- The two surfaces can still drift on the **policy-event semantic** and on
  **control reviews** — that is intentional and documented, not a bug.
- A future event-type addition must be made in BOTH queries (or deliberately in
  one, with a noted reason). The cross-reference comments make that obligation
  visible at the edit site.

## Revisit once in use

- If/when the dashboard and calendar unify policy-review semantics, reconsider
  a shared `upcoming_events` view as the single source.
- The calendar's default 90-day window means long-horizon audit periods and
  annual vendor reviews can fall outside the agenda even though they show on the
  dashboard's (unbounded, paginated) widget. If operators report "the dashboard
  shows it but the calendar doesn't," the window default — not the event source
  — is the lever. That is a separate tuning decision, not a source-parity bug.
