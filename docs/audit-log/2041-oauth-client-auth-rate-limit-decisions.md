# #2041 — rate-limit client authentication on /oauth/introspect and /oauth/revoke

#1913's sweep of unauthenticated endpoints found that `/oauth/introspect`
and `/oauth/revoke` run an argon2id client-secret verify for any presented
`client_id`, with no limit. `/oauth/token` already limits per `client_id`.
The build-time calls:

detection_tier_actual: manual_review
detection_tier_target: manual_review

## D1 — Reuse the token endpoint's bucket type; separate instances per endpoint

Both endpoints build a `clientAuthLimiter` from two `tokenBucketLimiter`s
(the token endpoint's type, unchanged). Each endpoint owns its buckets, as
the token endpoint owns its own. A shared instance would halve an attacker's
budget across the two endpoints, but would also couple two RFC surfaces
whose legitimate call rates differ (a resource server introspects far more
than it revokes). The per-IP bucket bounds total argon2 work per source
either way.

## D2 — Key on what is presented, before any lookup

Both buckets key on the presented `client_id` and the caller IP, checked
before `oauthclient.Store.Verify`. An unknown `client_id` is counted and
throttled exactly like a registered one, so the limiter reveals nothing
about which ids exist. `presentedClientID` copies the endpoints' credential
precedence exactly (Basic with both parts set, else the form `client_id`):
a looser match would let an empty Basic header plus form credentials reach
`Verify` uncounted. A unit test pins that case.

The per-IP bucket is checked first, so a throttled source cannot grow the
per-client map by cycling fresh ids.

## D3 — Client IP through TRUSTED_PROXY_CIDRS, injected

`internal/api/auth` imports this package, so `auth.ClientIP` cannot be
imported here. The endpoint configs take a `ClientIP func(*http.Request)
string`; `cmd/atlas` wires `authapi.ClientIP`. A nil resolver falls back to
the direct TCP peer, never a raw `X-Forwarded-For`.

## D4 — Defaults: 60/min per client_id, 120/min per IP

60/min matches `DefaultTokenRatePerMin`. The per-IP default is twice that,
so one host serving two clients is not throttled before either client is.
Both are configurable (`ATLAS_OAUTH_CLIENT_AUTH_RATE_PER_MIN`,
`ATLAS_OAUTH_CLIENT_AUTH_IP_RATE_PER_MIN`). No in-repo caller uses these
endpoints with client credentials today; a resource server that introspects
per request may need a higher per-client rate, noted in the CHANGELOG.

## D5 — The self-revocation bearer path is not counted

A revoke with no `client_id` authenticates by bearer JWT (ES256 verify, no
argon2) and is not the surface the ticket names. Counting it would let one
user's logout burst throttle others behind the same proxy.

## D6 — 429 body matches the token endpoint

`invalid_request` with `Retry-After`, the same body `/oauth/token` writes,
so clients see one throttling shape across the AS. RFC 7009 and RFC 7662
responses are unchanged for requests within the limit.

## D7 — Bound the limiter's memory, not just its rate (review of #2221)

The limiter's keys are caller-chosen, so the rate limit alone would trade
the CPU DoS for a memory one: megabyte `client_id`s, or rotated IPs, grow
the bucket maps without bound. Three bounds, all also applied to
`/oauth/token`:

- A presented `client_id` over `MaxClientIDBytes` (256; issued ids are
  36-byte UUIDs) is refused 401 `invalid_client` before any bucket is
  stored.
- `tokenBucketLimiter` prunes buckets that have refilled to full once the
  map reaches 4096 entries, then at twice the surviving count, the same
  scheme as the login throttle's `maybePrune`. A full bucket holds nothing
  a fresh one would not, so pruning never loosens the limit.
- The form body on the three endpoints is capped at 64 KiB
  (`MaxFormBytes`); net/http's default for urlencoded bodies is 10 MB.

A set-but-invalid rate env value now logs a Warn instead of falling back
silently.

## Not done here

`oauthclient.Store.Verify` returns early for an unknown or disabled
`client_id` without spending an argon2 verify, so response time still
differs between known and unknown ids on all three endpoints. The limiter
caps how fast that can be probed but does not remove it. Equalising it
belongs in `internal/auth/oauthclient` (the users store already does this
with `timingDummyHash`) and is filed as #2241.
