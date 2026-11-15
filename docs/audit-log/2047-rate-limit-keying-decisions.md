# #2047 — Per-replica limits, IPv6 /64 keying, TRUST_FORWARDED_HEADERS warning — decisions log

Follow-ups from the review of #2044 (#1913). This log does not block merge.

- detection_tier_actual: manual_review
- detection_tier_target: manual_review

(All three came out of PR review. None reached production.)

## D1 — Per-replica limits are documented, not shared

Recorded as D6 in `1913-login-throttling-decisions.md`, with the reasons
there. Operator-facing text: "Rate limits are per replica" in
`docs-site/docs/configuration.md`.

## D2 — One helper keys every per-IP limiter

`internal/api/ratekey.IP` turns a resolved client IP into the bucket key.
The login throttle, the OAuth client-auth limiter (#2041) and the admin
demo limiter all call it. It is a leaf package because
`internal/api/auth` imports `internal/api/oauth`, so it cannot live in
either.

- **IPv4-mapped IPv6 is unmapped first.** `::ffff:198.51.100.7` and
  `198.51.100.7` are one client on a dual-stack listener and share a
  bucket. Without this, a mapped address would be keyed on `::/64`
  together with every other mapped client.
- **Other IPv6 keys on its /64.** A /64 is one subnet, and end sites
  are assigned at least that much (RFC 6177 recommends between /48 and
  /64), so all of it is one source. A /56 or /48 would catch a client
  that holds a larger assignment, at the cost of putting more unrelated
  clients behind one bucket where a provider hands out /64s. /64 is the
  narrowest key the ticket asked for; widening it is a later call.
- **Zones are dropped**, so `fe80::1%eth0` and `fe80::1%eth1` cannot
  land in different buckets.
- **Unparseable input passes through unchanged.** `ClientIP` returns ""
  when it cannot parse an address, and the limiters keyed on "" before
  this change; they still do.
- **Only the key changes.** The session row and the demo audit event
  keep the full resolved address.

## D3 — TRUST_FORWARDED_HEADERS stays, with a louder warning

Removing it would break installs that set it and have not moved to
`TRUSTED_PROXY_CIDRS`. The startup warning already fired; it now says
what the flag does: any client can set `X-Forwarded-For` to choose its
session IP and its per-IP rate-limit key. The docs
(`configuration.md`, `deploy/docker/.env.example`) dropped "spoofing
vector unless a header-scrubbing proxy is in front", which was wrong for
a proxy that appends to the header: with every hop trusted the walk
returns the left-most entry, which the client wrote. Only a proxy that
replaces the header makes the flag safe.

Removal is left for a release that can carry a breaking-change note.
