# 566 — Per-kind email opt-out for the unmapped notification kinds

**Cluster:** Backend
**Estimate:** S (0.5-1d)
**Type:** JUDGMENT (event-taxonomy extension + UI toggle addition)
**Status:** `blocked` (depends on #542 — per-kind email filter — merged first)

## Narrative

Slice 542 layered the slice-108 per-event `email` channel ON TOP of the slice-445
master email opt-in: the digest includes a kind only if `(master ON) AND
(per-event email pref enabled)`. But the notification-`type` taxonomy and the
slice-108 `event` taxonomy are NOT 1:1. Two notification kinds the slice-445
digest renders have **no slice-108 event row at all**, so they have **no per-kind
opt-out surface**:

- `audit_note.reply` (audit-note replies)
- `evidence.staleness` (stale-evidence digests)

Slice 542 maps these as UNMAPPED → included-when-master-on (the safe,
backward-compatible default — see slice 542 D3). The consequence: an opted-in
user can mute control-drift / policy-ack / risk-review / audit-period-assignment
emails individually, but **cannot** mute audit-note-reply or stale-evidence
emails short of turning the master switch off entirely. A user who wants email
for everything EXCEPT noisy audit-note replies has no way to express that.

This slice gives those two kinds a per-kind opt-out by adding their event keys to
the slice-108 taxonomy and surfacing the toggles.

## Scope

1. Add `audit_note_reply` + `evidence_staleness` to the slice-108 event whitelist
   in BOTH places that must move together (the migration's
   `user_notification_preferences_event_check` CHECK constraint AND
   `internal/auth/userprefs.Events`) — a new migration sorting AFTER the latest
   (`20260607030000_backup_runs.sql` at filing time).
2. Add the two entries to slice 542's `kindToEvent` map
   (`internal/notify/email/kindfilter.go`) so the digest filter honors them
   (`audit_note.reply` → `audit_note_reply`, `evidence.staleness` →
   `evidence_staleness` — note the dot→underscore normalization, same shape as
   the existing `control.drift` → `control_drift` entry).
3. Add the two rows to the settings-page notification matrix
   (`web/app/(authed)/settings/page.tsx`) so users can toggle them, mirroring the
   four existing per-event rows.

## JUDGMENT surface

The event-key spelling (`audit_note_reply` vs keeping the dotted form) and
whether `evidence.staleness` warrants its own event vs folding into an existing
one. Default-on-missing-row stays (slice 108 D3 / slice 542 D2) so existing
opted-in users keep receiving both kinds until they explicitly opt out.

## Dependencies

- **#542** (per-kind email filter) — provides the `kindToEvent` map + the filter
  this extends; must merge first.
- **#108** (`user_notification_preferences`) — the event-taxonomy + UI matrix
  this extends.

## Anti-criteria (P0)

- Does NOT change the slice-542 default-on-missing-row semantics (an existing
  opted-in user must keep receiving both kinds until an explicit per-kind
  opt-out — no silent suppression).
- Does NOT change recipient resolution, tenant scoping, or the
  minimum-disclosure body shape (inherits P0-542-2 / P0-542-3).
- The migration CHECK constraint and `userprefs.Events` whitelist MUST land in
  the same slice (slice-108 schema-and-whitelist-move-together discipline).

## Notes

Parent: slice 542 (D3 forward note: "a future slice that adds a slice-108 event
row for `audit_note.reply` or `evidence.staleness` MUST also add the mapping
entry"). Filed as a spillover during the 542 build.
