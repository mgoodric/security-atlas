# 2587 — Needs attention: policy acknowledgements awaiting the caller: decisions log

Issue: mgoodric/security-atlas#2587 (from #2524, deferred by #2580).

## Detection-tier classification

- **detection_tier_actual:** none. No bug surfaced.
- **detection_tier_target:** none.

## Decisions

### D1 — the producer reads the ack store, not the calendar

The calendar's `policy_ack` events are one per policy version and lapse day,
counting many people, and cannot say who is still owed an ack. The producer
calls `policy.AckStore.PendingForUser`, the query behind
`GET /v1/me/acknowledgments`, with the caller `policyacks.CallerFromCred`
builds (exported for this; it was `ackCallerFromCred`). So the audience is
exactly the one the acknowledge route applies: live roles plus `everyone`
for a member (#2505, #2524, #2580).

### D2 — the admin flag is dropped

`PendingForUser` lists every policy for an admin, because an admin may
acknowledge any policy. In needs attention that would fill an admin's
for-me list with policies not aimed at them. The producer clears `IsAdmin`,
so an item is a policy whose audience includes the caller.

### D3 — bands

| State                                 | Band       | Date                                  |
| ------------------------------------- | ---------- | ------------------------------------- |
| Last ack older than 365 days (lapsed) | `overdue`  | `due_at` = last ack + 365 days        |
| Never acknowledged this version       | `due_soon` | no `due_at`; `since` = effective date |

A policy has no ack deadline of its own, so a first acknowledgement has no
due date ("the ack due date if one exists"). Calling it overdue the day the
policy is published would rank a new policy above real overdue reviews; due
soon, dated from the effective date, puts it after them and oldest first.
An ack that lapses within 14 days is not listed: it is still fresh, so it
is not pending, the rule `GET /v1/me/acknowledgments` applies too.

### D4 — type and gating

The item type is `policy_ack`, the calendar's name for the same thing, and
is a `?type=` value. It is listed only when the `policy` type is visible to
the caller (OPA read and the Policies module, as every policy item) and the
`policy_acknowledgments` feature is switched on, read strictly
(`SwitchedOnStrict`, #2590), so a flag-store error fails the request rather
than showing a switched-off feature.
