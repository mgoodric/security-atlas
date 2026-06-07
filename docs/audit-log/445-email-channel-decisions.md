# 445 — Email/SMTP notification channel — decisions log

JUDGMENT slice. The maintainer iterates post-deployment; this log records the
build-time calls Claude made, the confidence level, and the detection-tier
classification per slice 353.

## Decisions

### D1 — Provider-abstraction shape: one interface + one stdlib SMTP impl

`Provider` is a single-method interface (`Send(ctx, Message) error`) with one
concrete implementation (`SMTPProvider`) over the Go standard-library
`net/smtp`. **No** multi-provider plugin registry, **no** SES/SendGrid/Mailgun
adapters in v0. The slice spec ("Notes for the implementing agent") explicitly
calls for a thin abstraction; a plugin system is YAGNI for the single-VM
self-host target. A second provider, if ever needed, implements the same
interface — the interface is the seam, the registry is not.

- **Confidence:** High. The interface is the minimum seam that lets the
  integration test substitute an in-memory sink for a real SMTP server (AC-11).

### D2 — Minimum-disclosure digest body shape (P0-445-4 / AC-4 / AC-7)

The digest email carries ONLY:

1. A one-line greeting (no PII beyond "Hello").
2. Summary **counts** grouped by notification type (e.g. "3 audit-note
   replies, 1 control-drift alert"). Counts, not contents.
3. A single deep-link back to the authenticated app's notifications page.
4. A static footer explaining why they received it + how to opt out.

The body carries **no** notification `payload` values — no evidence IDs, no S3
URLs, no control text, no operator-entered note bodies. The type label is a
fixed, human-readable string mapped from the notification `type` constant (a
closed whitelist), never interpolated free-text. This is the strongest form of
minimum-disclosure: even the type label cannot carry injected content because
it is selected from a constant map, not echoed from the row.

- **Confidence:** High. Counts + deep-link is the canonical low-trust-channel
  pattern; detail stays behind auth.

### D3 — Header-injection guard: strip CRLF unconditionally + escape HTML (P0-445-2 / AC-6 / AC-12)

Two independent guards, applied at the message-build boundary:

- **Header fields (subject, recipient, From):** every `\r` and `\n` (and the
  lone `\r` / lone `\n` variants) is stripped UNCONDITIONALLY via
  `stripHeaderValue`. A notification-derived value containing
  `"Subject\r\nBcc: attacker@evil"` collapses to a single header line — no new
  header, no added recipient, no open-relay. Because D2 means header fields are
  built from constants (subject is a fixed template, recipient is the account
  email), the CRLF strip is defense-in-depth, but it is applied regardless so a
  future change that interpolates into a header inherits the guard.
- **HTML body:** every interpolated value is `html.EscapeString`- d. Since the
  body carries only counts (integers) + a server-built deep-link, the surface
  is already constrained; escaping is belt-and-suspenders for the type-label
  map and the deep-link host.

- **Confidence:** High. CRLF strip on headers is the textbook fix for the
  classic email-injection footgun.

### D4 — Recipient is the account email only (P0-445-1)

The recipient is resolved server-side via `GetUserByID(tenant, recipient_user_id)`
→ `users.email`. There is **no** request field, env var, or pref that lets a
user set a delivery address. A user cannot redirect another user's mail.

- **Confidence:** High.

### D5 — Idempotency via a delivery-log UNIQUE key (AC-5 / AC-15)

`email_delivery_log` has a UNIQUE constraint on
`(tenant_id, recipient_user_id, digest_key)`. The channel claims the key with
an `INSERT ... ON CONFLICT DO NOTHING RETURNING` BEFORE sending; if no row is
returned, the digest was already delivered (or is in flight) and the send is
skipped. `digest_key` is a deterministic per-period key (`"digest:" + UTC
date`), so re-running delivery for the same day is a no-op. Outcome
(sent/failed) + attempt count + last error are recorded on the same row.

- **Confidence:** Medium-High. The claim-before-send pattern is correct for
  single-process delivery; a multi-replica deployment relies on the UNIQUE
  constraint serializing the claim (the DB is the arbiter, not app memory).

### D6 — Rate-limit default: one digest per user per 24h

Delivery is digest-batched (not per-notification). The default cadence is **one
digest per user per 24 hours**, enforced by the per-day `digest_key` (a second
digest the same UTC day collides on the UNIQUE key). No separate token bucket
in v0 — the idempotency key IS the rate limit at the digest granularity, which
is the spec's intent ("digest-batched ... rate-limited per user").

- **Confidence:** Medium. 24h is a sensible default for a solo-operator inbox;
  revisit if operators want hourly/weekly cadence (see Revisit list).

### D7 — Master opt-in table, default OFF (P0-445-7 / AC-9)

A dedicated `email_channel_optin` table (one row per tenant+user, `enabled
DEFAULT false`) is the master switch. A user with **no row** reads as
opted-OUT (the channel's `IsOptedIn` returns false on missing row) — the
inverse of the slice-108 `user_notification_preferences` default-ON policy,
which is correct: low-trust email defaults off, in-app defaults on. The
existing per-event `email` channel in `user_notification_preferences` layers ON
TOP (an opted-in user still only receives types they have the email channel
enabled for) — but v0 ships the master switch only; per-event email filtering
is a follow-on.

- **Confidence:** High. Default-OFF is a hard P0; a separate table makes the
  default unambiguous (vs. trying to invert the slice-108 default).

### D8 — SMTP send timeout + backoff retry, not hot retry (AC-8)

`SMTPProvider.Send` dials with a context deadline (default 10s, env-tunable).
On a transient failure the channel records the failure + attempt count and
returns; the retry is driven by the next scheduled delivery tick (the digest
key is NOT claimed as "sent" on failure, so the next tick re-attempts) with the
attempt count available for an exponential backoff decision. No tight in-process
retry loop that could hammer a down SMTP server.

- **Confidence:** Medium. v0 records the failure + leaves the digest unclaimed
  for re-attempt; a dedicated backoff scheduler is a follow-on (the producer
  side, slice 439's wiring, owns the tick cadence).

### D9 — SMTP credentials env-only, never logged (threat-model S)

`Config.Username` / `Config.Password` come from `ATLAS_SMTP_USERNAME` /
`ATLAS_SMTP_PASSWORD` only. The Config struct's `String()`/log paths never emit
the password; errors returned from `Send` carry the SMTP server's response, not
the credential. No credential is written to the delivery log.

- **Confidence:** High.

## Revisit-once-in-use

- **Per-notification-kind email filtering** — v0 ships the master opt-in only.
  Layer the slice-108 per-event `email` channel on top once operators ask to
  mute specific types over email (follow-on, see spillover).
- **Digest cadence configurability** (D6) — 24h is hardcoded via the per-day
  key. If operators want weekly/hourly, the `digest_key` granularity becomes a
  per-user pref.
- **Backoff scheduler** (D8) — v0 leaves a failed digest unclaimed for the next
  tick. A real exponential-backoff scheduler with a dead-letter after N
  attempts is a follow-on; the `attempts` column is the foundation.
- **DKIM / SPF alignment** — the deployment's SMTP relay owns message
  authentication; the channel does not sign. Document for operators.
- **HTML + plaintext multipart** — v0 sends a single HTML part with a
  text-friendly structure. A proper `multipart/alternative` with a plaintext
  fallback is a polish follow-on.

## Detection-tier classification (slice 353, Q-13)

- `detection_tier_actual`: `none` — no bug surfaced during the slice.
- `detection_tier_target`: `none`. (Had the CRLF-injection guard been missing,
  the gap's target tier would be `integration` — AC-12 is an integration-tier
  proof — and a review catch would have been `actual=manual_review`.)
