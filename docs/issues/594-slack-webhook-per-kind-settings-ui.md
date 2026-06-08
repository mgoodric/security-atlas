# 594 — Settings UI for Slack + webhook per-kind notification toggles

**Cluster:** Frontend
**Estimate:** S (0.5-1d)
**Type:** code

**Status:** `ready` (depends on #583 — the backend filter + the widened
`user_notification_preferences.channel` whitelist — merged first)

## Narrative

Slice 583 generalized the slice-542 per-kind notification filter to the Slack +
webhook channels: the backend now narrows a user's Slack/webhook digest by the
slice-108 per-event `slack` / `webhook` channel preference, and the migration
`20260608020000_userprefs_slack_webhook_channels.sql` widened the
`user_notification_preferences.channel` CHECK to admit `'slack'` + `'webhook'`.
The generic `/v1/me/preferences` endpoint already accepts and returns these new
per-(event, channel) cells (it delegates validation to `userprefs.Channels`,
which slice 583 extended).

What slice 583 deliberately left out (scope: it is the FILTER consuming prefs,
not the UI for setting them): the web Settings → Notifications matrix is
hardcoded to render **four event rows × two channels** (`in_app`, `email`) — see
`web/app/(authed)/settings/page.tsx` (~line 971). So a user can set a Slack or
webhook per-kind opt-out only via the API today; the matrix shows no column for
them.

This slice adds the `slack` + `webhook` columns to that per-event preferences
matrix so a user can mute an individual notification kind per channel from the
UI, mirroring the existing `in_app` / `email` column pattern exactly.

## Acceptance criteria

- The Settings → Notifications per-event matrix renders a `slack` and a
  `webhook` column alongside `in_app` + `email`, for every event row.
- Toggling a cell PATCHes `/v1/me/preferences` with the
  `{event: {slack|webhook: bool}}` shape (existing endpoint; no new BFF route
  needed beyond the existing preferences proxy).
- Default-on display: a cell with no stored row renders enabled (mirrors the
  slice-108 default-on-missing-row read the matrix already uses).
- vitest covers the new column wire shape; the Playwright settings spec asserts
  the slack/webhook columns render and toggle.

## Anti-criteria (P0)

- Does NOT expose or accept a user-typed Slack/webhook URL or token — the
  per-kind matrix is a boolean opt-out grid only; the channel TARGET stays
  operator-configured env (slice-543 P0-543-2 SSRF boundary, unchanged).
- Does NOT change the master opt-in toggle rows (slice 584 owns those); this is
  the per-kind grid only. The master opt-in remains the OUTER gate.

## Dependencies

- **#583** — the backend per-kind filter + widened channel whitelist (parent).
- Composes with slice 584 (the master opt-in toggle rows) + slice 108 (the
  per-event preferences matrix this extends).

## Notes

Parent: slice 583. Out-of-scope spillover recorded by slice 583 (it kept
setting-of-prefs UI out of scope; this is that UI).
