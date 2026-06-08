# 583 — Per-kind filtering for Slack + webhook channels

**Cluster:** Backend
**Estimate:** S-M (1-2d)
**Type:** code (generalize slice-542 per-kind filter to the new channels)

**Status:** `blocked` (depends on #543 — Slack/webhook channels — merged first)

## Narrative

Slice 542 added per-notification-kind filtering for the EMAIL channel: on top of
the master opt-in, a user can mute individual notification kinds via the
slice-108 `user_notification_preferences` per-event `email` channel column
(`internal/notify/email/kindfilter.go`). Slice 543 shipped Slack + webhook with
the MASTER opt-in only (per-kind deferred, recorded in the 543 decisions log).

This slice generalizes the slice-542 filter to the new channels: add `slack` and
`webhook` channel values to the slice-108 per-event preference taxonomy (a
migration if `user_notification_preferences.channel` is constrained) and apply
the same `master AND per-kind`, default-on-missing-row composition the email
channel uses, factored into the shared `internal/notify` package so all three
channels share one filter implementation.

## Dependencies

- **#543** (Slack + webhook channels) — provides the channels this filter
  narrows.
- Composes with slice 108 (`user_notification_preferences`) + slice 542 (the
  per-kind email filter being generalized) + slice 566 (per-kind email prefs).

## Anti-criteria (P0)

- Does NOT change the recipient/target or tenant scoping — the filter operates
  purely on the in-memory, already-RLS-scoped count map (slice 542 P0-542-2).
- Default-on-missing-row preserved (no silent suppression of a kind).
- The master opt-in stays the OUTER gate (default opted-OUT); per-kind is the
  inner filter.

## Notes

Parent: slice 543 decisions-log "What this slice does NOT do".
