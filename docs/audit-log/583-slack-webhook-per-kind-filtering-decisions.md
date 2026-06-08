# Slice 583 — per-kind filtering for Slack + webhook channels: decisions log

**Slice type:** JUDGMENT (the channel-pref shape, the default-on semantics, and
the 542-generalization factoring are build-time calls recorded here, not blocked
on a human sign-off — the runtime AI-assist boundary is unrelated and untouched).

**Parent:** slice 543 ("Slack + webhook channels") decisions-log "What this
slice does NOT do" named per-kind filtering for the new channels as the deferred
follow-on; slice 542 shipped the same filter for EMAIL only, and slice 566
closed the last two unmapped email kinds. All four (108, 542, 566, 543) are
merged on `main`; the slice doc's "blocked on 543" line is stale.

This slice generalizes the slice-542 per-notification-kind EMAIL filter to the
slice-543 Slack + webhook channels so a user can mute specific notification
kinds per channel. It is purely a FILTER over the already-RLS-scoped in-memory
count map — it does NOT change the recipient/target or tenant scoping (slice 542
P0-542-2, inherited), and it preserves default-on-missing-row (no silent
suppression).

## Detection-tier classification (slice 353 Q-13)

- `detection_tier_actual`: `unit` — one build-time misfit was caught immediately
  at the compile/`unit` tier: the shared `FilterCountsByChannelPref` reassigns
  `counts` via `:=` inside each channel's DeliverDigest closure (declaring the
  new `total`), which `go build` validated as a same-scope redeclaration rather
  than a shadow. No bug escaped to a later tier.
- `detection_tier_target`: `unit` — and it was caught at compile (`unit`).
- No defect surfaced at the `integration`/`playwright`/`production` tiers.

## D1 — Channel-pref shape: reuse the slice-108 per-(event, channel) matrix; add `slack` + `webhook` channel values

**Decision:** rather than invent a new per-channel-per-kind table, extend the
existing slice-108 `user_notification_preferences` taxonomy — one row per
`(tenant, user, event, channel)` — by adding `'slack'` + `'webhook'` to the
`channel` whitelist. The migration
`20260608020000_userprefs_slack_webhook_channels.sql` widens the
`user_notification_preferences_channel_check` CHECK from `('in_app', 'email')`
to `('in_app', 'email', 'slack', 'webhook')`; the Go whitelist
`internal/auth/userprefs.Channels` extends in the same PR (whitelist-move-
together discipline, slice 108).

**Why.** The slice-108 matrix is already the per-event-per-channel preference
substrate; slice 542 layered the email filter on its `email` channel column. Two
more channels are two more channel values in the SAME matrix — no new table, no
new RLS surface, no new sqlc queries (the existing `ListUserNotificationPreferences`
already returns `(event, channel, enabled)` rows). The generic
`/v1/me/preferences` endpoint then accepts + returns the new cells for free,
because it delegates validation to `userprefs.Channels`. This is the minimum-
surface generalization the spec asked for.

**No sqlc / dbx change.** The filter consumes the existing
`ListUserNotificationPreferences` rows with a different channel discriminator;
no query was added, so `internal/db/dbx` is untouched and the sqlc-drift guard
stays clean.

## D2 — Default semantics: default-on-missing-row, master is the OUTER gate (mirrors 542/566)

**Decision:** identical composition to slice 542 — `master AND per-kind`, with
default-on-missing-row:

- The master channel opt-in (slice-543 `slack_channel_optin` /
  `webhook_channel_optin`, default opted-OUT) is the OUTER gate, enforced in each
  channel's `DeliverDigest` BEFORE the filter runs.
- The per-kind prefs are the INNER filter: a kind is suppressed for a channel
  ONLY by an EXPLICIT `enabled=false` row for `(event, channel)`. Absent row =
  inherit the master opt-in = deliver. Unmapped kind = deliver.
- The migration inserts NO rows, so it is backward-compatible: an opted-in user
  keeps receiving every kind on a channel they opted into until they set an
  explicit per-kind opt-out. No silent suppression of a previously-delivered
  kind.

**Why.** This is the safe composition (slice 542 JUDGMENT): it cannot start
delivering on a channel a user never opted into, and it cannot silently stop a
delivery a user expects. The widening is monotonic, so the migration applies
cleanly over existing data.

## D3 — 542-generalization factoring: lift the filter into shared `internal/notify`; email delegates

**Decision:** lift the slice-542 filter — the `kindToEvent` map, the per-kind
enable decision, the count-map narrowing, and the per-channel pref projection —
out of `internal/notify/email/kindfilter.go` into the shared `internal/notify`
package as channel-agnostic functions:

- `notify.KindToEvent(kind) (event, mapped)` — the single source of truth for
  the kind→event taxonomy (dot→underscore normalization).
- `notify.EnabledForKind(kind, channelByEvent)` — the per-kind decision GIVEN
  the master opt-in is ON (channel encoded in the projected map).
- `notify.FilterCountsByChannelPref(counts, channelByEvent)` — narrows the
  in-memory count map + recomputes the total.
- `notify.ChannelPrefMap(rows, channel)` — projects slice-108 rows to ONE
  channel as `event -> enabled` (the generalization vs slice 542's email-only
  projector).

The Slack + webhook channels consume these directly via a one-line dbx→
`ChannelPrefRow` adapter (`channelPrefMap` in each package). The email package's
`emailEnabledForKind` / `filterCountsByEmailPref` / `kindToEvent` become thin
delegates to the shared functions, so email's call sites stay byte-identical
(slice 543 D1) AND there is exactly ONE filter implementation (the spec's
"factored into the shared internal/notify package so all three channels share
one filter implementation"). The existing slice-542 email unit tests pass
unchanged — proof the delegation preserved behavior.

**Why.** A copy-per-channel filter is the drift hazard slice 543 D1 already
warned about. One shared implementation with three thin channel-specialized
delegates keeps the email wire output identical while making slack/webhook reuse
the proven logic. The `ChannelPrefRow` value type keeps `internal/notify`
decoupled from `internal/db/dbx` (the package owns no DB import).

## D4 — Scheduler path honored for free

**Decision:** apply the filter INSIDE each channel's `DeliverDigest`, not in the
slice-582 scheduler. The scheduler's `deliverOne` calls
`ch.Deliverer.DeliverDigest(...)`, so applying the filter in the sink means the
scheduled fan-out honors it automatically — no scheduler edit, no second filter
call site.

**Why.** Single application point = no drift between the on-demand path and the
scheduled path. The scheduler stays a pure driver (slice 582 P0).

## Anti-criteria honored (P0)

- **No recipient/target or tenant-scope change (slice 542 P0-542-2).** The
  filter operates purely on the already-RLS-scoped in-memory count map; the
  `ListNotificationsForUser` + opt-in + claim + send path is byte-unchanged.
- **Per-channel isolation.** An EMAIL opt-out for a kind does NOT mute it on
  Slack/webhook (proven by `TestSlackDeliver_PerKindFilter_EmailOptOutDoesNotMuteSlack`
  - the webhook analog) — `ChannelPrefMap` projects to exactly one channel.
- **Default opted-OUT master stays the OUTER gate.** The per-kind filter runs
  AFTER the master opt-in check; it can only NARROW, never widen, delivery.
- **Slice-543 SSRF / secret boundaries untouched.** This slice filters WHICH
  kinds deliver, not how; no transport, config, URL, or secret code changed.

## What this slice does NOT do (scope boundary)

- **No settings UI for the slack/webhook per-kind toggles.** The web
  Settings → Notifications matrix renders only `in_app` + `email` columns
  (`web/app/(authed)/settings/page.tsx`); the new per-kind prefs are settable
  via the generic `/v1/me/preferences` API but have no dedicated UI column yet.
  Filed as spillover **slice 594**. (This slice is the FILTER consuming prefs;
  the UI for setting them is the spillover — matching the batch-218 scope
  boundary that keeps `internal/api/me` + the web settings page off this slice,
  which slice 585 touches.)
- **No edit to `internal/api/me`.** The generic preferences endpoint exposes the
  new cells for free via `userprefs.Channels`; no me-handler file was touched
  (585's territory).

## Verification

- `go build ./...` clean.
- `go test ./internal/notify/...` + `./internal/auth/userprefs/...` green
  (unit), including the unchanged slice-542 email tests.
- `go test -tags=integration -p 1 ./internal/notify/...` green against a real
  Postgres with the migration applied; the four per-channel semantics
  (master-on+kind-on → deliver, master-on+kind-off → mute, master-on+no-row →
  default-on deliver, email-unchanged) proven per new channel.
- Migration applied + reversed + re-applied cleanly (widened CHECK confirmed;
  `slack`/`webhook` accepted, `sms` rejected).
- `golangci-lint run` (incl. `--build-tags=integration`) clean on touched
  packages; `gofmt -l` clean; no dbx/sqlc drift.
