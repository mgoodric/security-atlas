# Slice 543 — additional notification channels (Slack + webhook): decisions log

**Slice type:** JUDGMENT (channel-abstraction shape + per-channel disclosure +
secret/SSRF handling are build-time calls recorded here, not blocked on a human
sign-off — the runtime AI-assist boundary is unrelated and untouched).

**Parent:** slice 445 (email delivery substrate, merged). This slice
generalizes 445's opt-in + notification-store-as-source + claim-before-send +
minimum-disclosure pattern to two more delivery sinks.

## Detection-tier classification (slice 353 Q-13)

- `detection_tier_actual`: `none` — no bug surfaced during the build that
  escaped the tier it should have been caught in. Two build-time misfits were
  caught immediately by their target tier: (a) a Slack-escaping assertion wrote
  literal `&amp;` where JSON further escapes `&` → `&`, caught by the
  `unit` tier (`TestBuildMessage_Escapes`) and fixed by decoding the JSON before
  asserting; (b) `fmt.Sprintf("%s", secret)` tripped staticcheck S1025, caught
  by the `unit`/lint tier and rewritten to `secret.String()`.
- `detection_tier_target`: `unit` for both — and both were caught at `unit`.

## D1 — Channel abstraction: sibling packages over a plugin registry

**Decision:** add `internal/notify/slack` and `internal/notify/webhook` as
SIBLING packages that mirror `internal/notify/email`'s `Channel` orchestration,
plus a small shared `internal/notify` parent package for the genuinely
cross-cutting pieces. Do NOT extract a single `Channel`/`Provider` interface
that email is refactored onto.

**Why.** Slice 445 deliberately did NOT build a plugin registry ("thin by
design; no plugin registry", `email/provider.go`). The spec asked this slice to
revisit that "with a second real consumer in hand". With two more consumers in
hand the answer is still: a heavy registry is YAGNI for the single-VM self-host
target. The duplication between the three `Channel.DeliverDigest` orchestrators
is ~40 lines of opt-in → list → claim → build → send → record that reads
identically in each package; collapsing it behind an interface would force a
shared `Digest`/recipient abstraction that email's account-email recipient and
Slack/webhook's no-recipient (operator target) models do not actually share.
The **shared** parts that DO generalize cleanly are pulled into `internal/notify`:

- `Summary` — the minimum-disclosure value object (counts + deep-link).
- `TypeLabel` — the CLOSED type→label map (kept in sync with email's copy).
- `DeepLink`, `DigestKeyForDay`, `Plural` — pure helpers.
- `Secret` — the redaction type (D3).
- `SSRFPolicy` — the webhook SSRF guard (D4).

**Email is untouched at runtime:** email keeps its own copies of the label map +
deep-link builder so its wire bytes stay byte-identical; its full unit +
integration suites pass unchanged (verified). The shared `internal/notify`
values are kept consistent with email's so the three channels render the same
wording.

**Per-channel idempotency.** A single `channel_delivery_log` table backs both
new channels, discriminated by a `channel` column folded into the UNIQUE
idempotency key `(tenant_id, channel, recipient_user_id, digest_key)` and into
the `notify.DigestKeyForDay(channel, ...)` key, so slack + webhook + email claims
never collide. Email keeps its own slice-445 `email_delivery_log` table
(untouched).

## D2 — Per-channel disclosure shape (minimum disclosure, P0-543-1)

**Slack:** a Block Kit message (`blocks` + a plain-text `text` fallback) with
two sections — a counts list and a deep-link line `<url|text>`. Counts come from
the closed `TypeLabel` map; the raw notification `type` string NEVER reaches the
payload. All interpolated text is escaped for the Slack message-text context
(`&`→`&amp;`, `<`→`&lt;`, `>`→`&gt;`, in that order). No notification body,
evidence, S3 key, or control text — counts + a single deep-link only, the same
discipline as 445's email digest.

**Webhook:** a flat JSON `Payload{source, event, total_unread, counts,
deep_link}` where `counts` is keyed by the closed human label. The payload is
built by the stdlib JSON encoder (no string interpolation into the wire), so
there is no injection surface — every value is encoded as a JSON value.

Both reject an empty summary (no zero-count message is ever sent).

## D3 — Secret handling (P0-543-5)

A shared `notify.Secret` (a `string` newtype) wraps every credential: the Slack
webhook URL (it carries a token in its path), the webhook bearer token, and the
webhook HMAC signing secret. `Secret` implements `fmt.Stringer`,
`fmt.GoStringer`, and `encoding.TextMarshaler` so that `%s`, `%v`, `%q`, `%#v`,
`%+v`, and `json.Marshal` ALL render `<redacted>` — the plaintext is reachable
only through `Reveal()`, which is called solely at the transport boundary (set a
header / compute an HMAC) and never logged. `notify.ScrubSecret` is
defense-in-depth: any error string that might echo a header value back from a
third party is scrubbed of every configured secret before it is returned or
persisted to the delivery ledger's `last_error`. `Config.Redacted()` on each
channel renders a log-safe one-liner. Env-only config via `ATLAS_SLACK_*` /
`ATLAS_WEBHOOK_*` (the `ATLAS_*` platform convention, mirroring email's
`ATLAS_SMTP_*`). Test fixtures use NEUTRAL values (`test-slack-token`,
`test-bearer-secret`) — no real-looking `xoxb-` tokens (GitGuardian
branch-scoped).

## D4 — Webhook SSRF guard (threat-model DOMINANT, P0-543-2)

Two legs:

1. **No user-controlled target.** The webhook URL is OPERATOR-configured (env),
   never per-user free-text, and never derived from notification content. The
   per-user opt-in tables carry only an `enabled` flag — there is no
   user-supplied target column to abuse. This alone removes the
   per-notification SSRF vector.
2. **Deny-internal validation at construction.** `notify.SSRFPolicy` validates
   the configured URL when the transport is built (startup), failing fast and
   visibly rather than silently at send time. The strict production policy
   (`webhook.SSRFPolicy()`): scheme MUST be `https`; the host MUST NOT
   resolve to an internal/non-routable address — loopback, unspecified,
   link-local (incl. the `169.254.169.254` cloud-metadata IP and `fe80::/10`),
   RFC1918 + ULA (`IsPrivate`), multicast, and CGNAT (`100.64.0.0/10`,
   explicitly denied since the stdlib helpers do not flag it). A literal-IP host
   is checked directly; a DNS name is resolved and EVERY returned address must
   be public (the mixed public+private "rebind" case is denied). The policy
   exposes test-only `AllowHTTP`/`AllowLoopback` knobs so the test harness can
   target a local `httptest` server WITHOUT loosening the production default —
   even with both knobs on, `169.254.169.254` is still denied.

When validation fails at startup, the HTTP server logs the rejection and wires
an INERT transport (the opt-in toggle still needs a channel object) — the
channel is disabled rather than pointed at an internal service.

## D5 — Opt-in + idempotency (mirror 445)

Per-channel master opt-in, **default opted-OUT** (P0-543-3): a user with no row
reads `enabled=false`. The only path that flips it on is the authenticated
`PUT /v1/me/{slack,webhook}-channel` — tenant + user come from the credential,
so there is no path to configure another user (threat-model E). Idempotency is
the slice-445 claim-before-send: `ClaimChannelDigest` does
`INSERT ... ON CONFLICT DO NOTHING`; a same-period second claim returns no row
and the channel skips the send (no double-send; the per-UTC-day digest_key is
the 24h rate-limit). A failed send records `outcome=failed` + `last_error` and
leaves the digest re-attemptable on the next tick (no hot retry, 445 D8 analog).

## D6 — Tenant isolation (invariant #6)

Every read + write runs in a tenant-scoped tx (`tenancy.ApplyTenant` at tx
start); all three new tables carry the canonical four-policy split under ENABLE

- FORCE ROW LEVEL SECURITY. Delivery reads the target user's notifications +
  opt-in under the notification's OWN tenant GUC, so tenant A's notifications can
  never deliver under tenant B's context — proven by the `NoCrossTenant`
  integration tests (asking channel A, under tenant A's GUC, to deliver tenant B's
  user posts nothing).

## What this slice does NOT do

- No scheduler that fans `DeliverDigest` out over all opted-in users — like
  445, the channel is the substrate; the per-user driver/cron is a follow-on
  (spillover, see slice doc). The opt-in toggles + the delivery primitive are
  the slice.
- No per-kind filtering for the new channels (445's slice-542 per-kind email
  filter is email-only); the new channels honor the master opt-in only for v0.
  A per-kind generalization is a follow-on.
- No web settings UI toggle wiring beyond the BFF-less `GET/PUT` routes — the
  routes + the opt-in store are shipped; surfacing them in the settings page is
  a follow-on.
