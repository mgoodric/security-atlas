# 585 — Disabled "channel not configured" state for delivery toggles

**Cluster:** Frontend (+ small backend wire change)
**Estimate:** S (0.5d)
**Type:** code

**Status:** `ready` (depends on #543's channel routes + #584's toggle UI — both
merged; this slice extends both)

## Narrative

Slice 584 added the Slack + webhook master opt-in toggles to Settings →
Notifications (and slice 445 added the email toggle before it). All three
toggles render **unconditionally** and are always interactive, regardless of
whether the deployment has actually configured the channel's delivery target
(SMTP for email; `ATLAS_SLACK_*` env for Slack; `ATLAS_WEBHOOK_*` env for
webhook).

Slice 584's spec asked for a toggle to render **disabled with an explanatory
note** when the deployment has not configured the channel ("env unset → inert"),
"mirroring how the email toggle behaves when SMTP is unconfigured." During 584's
build this was found to rest on a false premise: **the slice-445 email toggle has
no such disabled-when-unconfigured behavior** — it renders the toggle
unconditionally — and the backend wire for all three channels
(`GET /v1/me/{email,slack,webhook}-channel`) returns **only** `{enabled: bool}`,
with **no `configured` signal**. There is therefore nothing for the UI to read
to decide whether to disable a toggle. Slice 584 mirrored the actual email
pattern (toggle always rendered) and deferred the disabled-state behavior here.

A user who opts into a channel the operator has not configured currently gets a
silent no-op: the opt-in persists but no message is ever delivered (the channel's
`ErrNotConfigured` path makes delivery inert server-side). Surfacing the
unconfigured state would close that "why am I not getting Slack messages?"
confusion gap.

## Scope

1. **Backend (slice 543's domain — small additive wire change):** extend the
   `GET /v1/me/{email,slack,webhook}-channel` response to carry a
   `configured: bool` field alongside `enabled`, derived from the channel's
   existing config-from-env (`slack.Config` / `webhook.Config.Enabled()` /
   the email channel's SMTP-configured check). Additive only — `enabled`
   stays; PUT body unchanged (`{enabled}`).
2. **Frontend (extends slice 584's `ChannelMasterToggle`):** when
   `configured === false`, render the toggle **disabled** with a muted
   explanatory note ("This channel is not configured by your administrator").
   Apply to all three rows (email + Slack + webhook) for consistency.
3. **Tests:** vitest for the BFF passthrough of the new field; Playwright AC
   asserting the disabled+note state when the channel is unconfigured (needs a
   CI-satisfiable way to bring the stack up with the channel env unset — likely a
   second e2e variant or a route-level assertion, since the default
   docker-compose may configure none/all channels).

## Anti-criteria (P0)

- Does NOT expose the operator-configured target/secret in the new field — only
  a boolean `configured`, never the URL/token (preserves P0-543-2 / SSRF
  boundary).
- Does NOT change the PUT contract or the opted-OUT default (P0-543-3).

## Notes

Parent: slice 584 (`docs/issues/584-notification-channel-settings-ui.md`) — the
toggle UI this extends. Sibling premise correction noted in 584's build: the
"mirror the email toggle's unconfigured behavior" instruction could not be met
because that behavior did not exist; 585 introduces it for all three channels at
once.
