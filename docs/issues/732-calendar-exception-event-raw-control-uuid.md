# 732 — Calendar/dashboard exception event labels show the raw control UUID, not the SCF code + name

**Cluster:** Platform / copy (backend)
**Estimate:** S (0.5d)
**Type:** JUDGMENT (copy authorship + a backend query JOIN)
**Status:** `ready`

## Narrative

Spillover from parent slice **670** (pre-GA copy & metadata pass), finding **ATLAS-035 / AC-6**. The 2026-06-10 empty-tenant UI audit found that exception expiration events on `/calendar` (month grid, agenda) and on the `/dashboard` "Upcoming" panel render their title as the raw control UUID:

> Exception on control 32e55da9-…

The desired user-facing form is the SCF anchor code plus the control name, matching the ATLAS-012 fix for the control-detail empty state:

> Exception on AAA-01 — <control name>

Slice 670 fixed every copy/metadata defect that lived in `web/` (titles, breadcrumbs, the control-detail empty-state UUID, internal-jargon sweep). ATLAS-035 could NOT be landed in 670 because the offending title is **constructed in the backend**, and 670 was scoped to `web/`-only with an explicit "no backend/Go changes" anti-criterion.

The title is built in SQL at `internal/db/queries/calendar.sql` (the exceptions branch of the unified-calendar UNION):

```sql
('Exception on control ' || e.control_id::text)::text  AS title,
```

The frontend (`web/components/calendar/month-grid-view.tsx`, `web/components/calendar/agenda-view.tsx`, `web/components/dashboard/upcoming-panel.tsx`) renders `event.title` verbatim — it has no control metadata to substitute, so the fix MUST happen in the query. This makes it a backend slice, not a `web/` copy slice.

## Threat model

None — user-facing label copy. No data/scope/wire/authz change. The query JOINs only tables already inside the tenant's RLS-scoped visibility; the control row is already tenant-scoped, so no new data crosses a tenant boundary.

## Acceptance criteria

- [ ] **AC-1.** The exceptions branch of `internal/db/queries/calendar.sql` JOINs the control (and its SCF anchor) so the event `title` reads `Exception on <scf_code> — <control name>` instead of `Exception on control <uuid>`.
- [ ] **AC-2.** When the control has no resolvable SCF anchor code (data edge case), the title falls back gracefully (e.g. the control bundle id or name) — it never prints a bare UUID.
- [ ] **AC-3.** sqlc is regenerated (`internal/db/dbx/calendar.sql.go`) and the handler (`internal/api/calendar/handler.go`) passes the new title through unchanged (no handler-side string-building — the SQL is the single source of truth, consistent with the audit/policy branches).
- [ ] **AC-4.** The `/calendar` ICS export (`internal/api/calendar/ics.go`) inherits the improved `SUMMARY` automatically (it reads `row.Title`); add/extend a unit test asserting the SUMMARY no longer contains a raw UUID for an exception event.
- [ ] **AC-5.** Integration test in `internal/api/calendar/integration_test.go` seeds an exception on a control with a known SCF code and asserts the returned event `title` contains the code + name and NOT the control UUID.
- [ ] **AC-6 (JUDGMENT — decisions log).** Record the exact title template chosen (separator, ordering, the no-anchor fallback shape) and how it composes with the ATLAS-012 control-detail empty-state wording for cross-surface consistency.

## Anti-criteria

- Does NOT change the calendar event wire shape (`title` stays a single `string` field); the improvement is its CONTENT only.
- Does NOT touch the audit / policy / vendor / control branches of the UNION — exception branch only.
- Does NOT build the title in Go; the SQL stays the single source of truth (parity with the existing branches).

## Dependencies

- Backend: `internal/db/queries/calendar.sql`, `internal/db/dbx/calendar.sql.go` (regen), `internal/api/calendar/{handler.go,ics.go}`.
- None blocking — the control + SCF anchor tables already exist and are joined elsewhere (e.g. the controls coverage query).

## Notes

Source: 2026-06-10 empty-tenant browser audit, item **ATLAS-035**. Parent slice **670** (ATLAS-035 / AC-6). Same raw-UUID-in-copy class as ATLAS-012 (control-detail empty state, fixed in 670), different surface (`/calendar`, `/dashboard`) and different layer (backend SQL vs `web/` copy).
