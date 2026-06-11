# 675 — Calendar agenda missing audit-period / vendor-review / policy-review events (vs dashboard)

**Cluster:** Calendar
**Estimate:** S-M (0.5-1.5d)
**Type:** AFK
**Status:** `ready` — surfaced by the 2026-06-10 demo-tenant UI audit (ATLAS-034).

## Narrative

The dashboard "Upcoming" widget lists audit periods + ~13 vendor reviews + 2 exceptions, but
the **Calendar agenda (all filters checked) shows ONLY the 2 exceptions** — no audit periods,
vendor reviews, or policy reviews. Re-verified on `main` build `2a3805b`. Two views of "what's
coming up" disagree; the calendar legend even **lacks a "Vendor reviews" type** that the
dashboard includes. So the calendar's event-sourcing is narrower than the dashboard's.

## Threat model

Read-only, tenant-scoped. No new data/scope/wire. The fix aligns the calendar's event
sources with the dashboard's (and the `.ics` feed) so the two agree.

## Acceptance criteria

- [ ] **AC-1.** The Calendar agenda sources the SAME event types as the dashboard "Upcoming"
      widget: audit-period boundaries, vendor reviews, policy reviews, and exceptions.
- [ ] **AC-2.** The calendar legend includes every surfaced type (add "Vendor reviews" etc.).
- [ ] **AC-3.** Determine the source of the divergence (the calendar and dashboard should
      share one upcoming-events query/model — DRY it if they don't) so they cannot drift again.
- [ ] **AC-4.** The `/v1/calendar.ics` feed (if it sources events) stays consistent with the
      in-app agenda.
- [ ] **AC-5.** Test: a seeded tenant with audit periods + vendor reviews + exceptions shows
      all of them in the agenda (was: exceptions only).

## Anti-criteria

- Does NOT change what the dashboard shows (the dashboard is the more-complete reference here).
- Does NOT add new event types beyond what the dashboard/program already tracks.

## Dependencies

- The compliance calendar event sourcing (`web/app/(authed)/calendar`, the calendar events API) + the dashboard "Upcoming" model.

## Notes

Source: 2026-06-10 demo-tenant audit, item **ATLAS-034** (medium/major). Re-tested open on
`2a3805b`. Relates to slice 668 (calendar today-highlight) — same surface.
