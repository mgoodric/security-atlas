# #2234 — calendar follow-ups: export type filter, policy-ack grouping and scope

Follow-ups from the delta review of #2230 (#2130). Builds on
`docs/audit-log/2130-calendar-single-source-decisions.md` (D8, D9).

detection_tier_actual: none
detection_tier_target: none

## D1 — Subscriber-bound ICS tokens are deferred to #2331

The ticket's first item binds each ICS token to its subscriber and
evaluates OPA as that person. That makes a key's minter an authorization
input, where `minted_by` is attribution only today. That is Matt's call,
and it is filed as #2331. The ICS feed keeps leaving out every gated type
(fail closed), as #2130 D8 and #2232 set.

## D2 — The export's upcoming panel uses the calendar's type filter

`calendar.ReadableTypes` is now an exported function. The JSON calendar
and the dashboard export both call it, and the export calls it through
`dashboardexport.UpcomingTypesFor`. The handler passes the result to
`PanelSource.Snapshot`, and a caller who may read no type gets an empty
panel. With no engine wired, every type stays, as on the calendar. The
export's own `IsAdmin || IsApprover` gate is unchanged: it decides who may
export, not which events they see.

Finding, not fixed here: the flag gate admits only credentials whose flags
derive `admin` or `grc_engineer`, and the engine merges `user_roles` into
the derived set, so no caller the gate admits loses an event today. The
filter holds by construction for when the gate moves to OPA.
`TestUpcomingPanel_MatchesCalendarPerRole` therefore runs the five roles
through `UpcomingTypesFor` and the live source with a flagless caller, and
`TestExportHandler_UpcomingMatchesCalendar` covers the handler end to end
for an approver. The JWT middleware sets `IsAdmin`/`IsApprover` only for a
super admin, so a tenant admin signed in by session gets 403 on the
export. That is a separate question for the export's gate.

## D3 — Policy acknowledgements are grouped per policy and lapse day

One row per user per policy version let a tenant-wide ack campaign fill
the 500-row cap and "Coming up". The branch now emits one `policy_ack`
event per (policy version, UTC day the acks lapse):

| Field                         | Value                                                                                        |
| ----------------------------- | -------------------------------------------------------------------------------------------- |
| `id`                          | `<policy_version_id>:<YYYY-MM-DD>`, stable across fetches, so the ICS UID still dedupes      |
| `title`                       | `<policy title>: N acknowledgements lapse on <date>` (`1 acknowledgement lapses on` for one) |
| `starts_at`                   | the earliest lapse that day                                                                  |
| `related_entity_kind` / `_id` | `policy` / the policy version id (was the ack)                                               |
| `summary`                     | the policy title                                                                             |

Grouping by day, not by policy across the window, keeps "lapse on
<date>" true and the status (`duedate.Status` on `starts_at`) right for
every ack in the group. A campaign spread over a week is up to seven rows,
not hundreds. The web app treats `policy_ack` as unlinked text, so the new
id and related entity change nothing there. API consumers that read the
ack id from `related_entity_id` now get the policy id.

## D4 — Only live acks count

- **Policy version:** an ack counts only when its version is `published`.
  Policies have no separate retired state: the status set is draft,
  under_review, approved, published and superseded. A superseded
  version's acks are dropped, because the new version needs its own ack.
- **User:** an ack counts only when its user has `status = 'active'` and
  `active` (the SCIM flag). That is the membership predicate
  `admin_users.sql` uses. A deactivated user's lapse is not work anyone
  has to chase.

Both are inner joins inside the "latest ack per (version, user)" step, so
a deactivated user's older ack never stands in for their latest one.
