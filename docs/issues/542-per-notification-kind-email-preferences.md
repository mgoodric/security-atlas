# 542 — Per-notification-kind email preferences

**Cluster:** Backend
**Estimate:** S (0.5-1d)
**Type:** JUDGMENT (filter semantics + default-on-vs-off per kind)
**Status:** `blocked` (depends on #445 — email delivery substrate — merged first)

## Narrative

Slice 445 shipped a **master** email opt-in (one toggle: email delivery on/off
for the whole user). It deliberately deferred per-notification-kind email
filtering ("v0 ships the master switch only" — slice 445 D7). The slice-108
`user_notification_preferences` table already carries a per-event `email`
channel column, but slice 445 does NOT consult it — an opted-in user receives a
digest covering ALL their unread notification kinds.

A user who wants email for audit-note replies but NOT for control-drift alerts
has no way to express that today. This slice layers the slice-108 per-event
`email` channel ON TOP of the 445 master opt-in: the digest includes a
notification kind only if (master opt-in is ON) AND (that kind's `email` channel
pref is enabled). The master switch stays the outer gate (default off); the
per-kind prefs are the inner filter (slice-108 default-on-missing-row).

The hard JUDGMENT this slice owns: the **filter composition semantics** (master
AND per-kind, vs master OR per-kind — master-AND is the safe default: a user
must opt in once at the master level before any per-kind pref matters), and the
mapping from notification `type` constants to slice-108 event keys (they are not
1:1 — e.g. `audit_note.reply` has no slice-108 event row; unmapped kinds default
to "included when master is on").

## Threat model

Inherits the slice 445 + slice 108 threat models. No new external surface; the
filter is applied inside the existing RLS-scoped digest build.

- **I — Information disclosure.** The filter must not change WHICH user receives
  the digest — only WHICH kinds appear in it. The recipient resolution (account
  email only) and tenant scoping are unchanged.
  **Mitigation:** the filter operates on the in-memory type-count map AFTER the
  RLS-scoped read; recipient + tenant path are untouched. A test proves a
  per-kind mute removes that kind's count but does not redirect delivery.
- **E — Elevation of privilege.** A user can only set their own per-kind prefs
  (slice-108 already enforces this).

## Acceptance criteria

- [ ] The digest build consults the slice-108 per-event `email` channel and
      omits a kind whose `email` pref is disabled (master opt-in still required).
- [ ] Unmapped notification kinds default to included-when-master-on; mapping
      documented.
- [ ] Composition semantics (master AND per-kind) documented + tested.
- [ ] A muted kind's count is removed from the digest but delivery is otherwise
      unchanged (integration test).
- [ ] Decisions log + changelog entry.

## Anti-criteria (P0 — block merge)

- **P0-542-1.** Does NOT bypass the slice 445 master opt-in (master stays the
  outer gate; default off).
- **P0-542-2.** Does NOT change recipient resolution or tenant scoping.
- **P0-542-3.** Does NOT change the minimum-disclosure body shape.

## Dependencies

- **#445** (email delivery substrate) — provides the digest build to filter.
- **#108** (`user_notification_preferences`) — provides the per-event `email`
  channel column (already exists).

## Notes

Parent: slice 445 ("Follow-on slices: ... per-notification-kind email
preferences"; D7 Revisit list). Filed as a spillover during the 445 build.
