# Slice 585 — decisions log (type: code, light)

Disabled "not configured" state for the notification-channel toggles. Type is
`code`; only the build-time calls that were genuinely judgment (not mechanical)
are recorded here.

## D1 — `configured` is captured at construction, not read per-request

The handler does not call into `internal/notify` on the request hot path to ask
"is this channel configured?". Instead `httpserver.go` passes the
config-presence boolean (`Config.Enabled()`) into the handler constructor once at
startup. Rationale: (a) the operator env is fixed for the process lifetime, so a
per-request lookup buys nothing; (b) it keeps the change inside
`internal/api/me` + `httpserver.go` and avoids touching `internal/notify` (slice
583's domain, parallel this batch). The handler stores a plain `bool` — it never
holds the channel target, so there is no path for the secret to reach the wire.

## D2 — webhook `configured=false` when present-but-SSRF-invalid

`webhook.Config.Enabled()` is true whenever the env URL is non-empty. But
`httpserver.go` already falls back to an INERT transport when the URL fails the
startup SSRF guard (a present-but-internal-target URL). In that state the channel
is configured-on-paper but NOT usable. Reporting `configured=true` would render
an enabled toggle for a channel that can never deliver — the exact "silent no-op"
confusion this slice exists to close. Decision: report
`configured = Config.Enabled() && webhookErr == nil`, so the toggle stays
disabled for an unusable webhook. Email and Slack have no equivalent
construction-time validation, so for them `configured == Config.Enabled()`
directly.

## D3 — `configured` applied to all three channels (email included)

The slice spec (Scope item 2) asks for the disabled state on all three rows
(email + Slack + webhook) "for consistency", and notes the slice-445 email toggle
never had the behavior. Both the generic `ChannelMasterToggle` (Slack/webhook)
and `EmailChannelMasterToggle` were updated identically. The shared copy is
"This channel is not configured by your administrator."

## D4 — frontend treats missing `configured` as configured (backward-tolerant)

The TS type is `configured?: boolean`. The toggle disables ONLY on an explicit
`configured === false` from a settled query, never on `undefined` (loading, or an
older backend). This preserves the prior always-interactive behavior under
partial rollout and avoids flashing the unconfigured note while the query is in
flight.

## Detection-tier classification

- `detection_tier_actual`: `none` — no bug surfaced during the build.
- `detection_tier_target`: `none`.

The new behavior is covered at the tier it belongs to: the `configured`
true/false signal + the secret-never-leaks property at the Go unit tier
(`internal/api/me`), the BFF passthrough at the vitest tier (all three channel
routes), and the disabled-toggle + note at the Playwright tier (route-injected
`configured=false`).
