# 2241 — Constant-cost `oauthclient.Store.Verify`: decisions log

Issue: mgoodric/security-atlas#2241. `Store.Verify` returned early for an
unknown, empty or disabled `client_id` without any argon2id work, while a
known one cost a full verify. Response time on `/oauth/token`,
`/oauth/introspect` and `/oauth/revoke` therefore told an attacker which
client_ids exist. Every rejection now costs one argon2id verify.

## Detection-tier classification

- **detection_tier_actual:** manual_review. Found by reading the code while
  building #2041 (#2221).
- **detection_tier_target:** unit. `TestAuthenticateRunsOneVerifyOnEveryPath`
  now counts verifier calls on every path.

## Decisions

### D1 — reuse the `users.timingDummyHash` shape, built by `password.Hash`

The dummy hash is a package-level `sync.OnceValue` built by `password.Hash`,
the same function `issue` uses for real client secrets. There is no separate
parameter config to read: the argon2id parameters are constants in
`internal/auth/password`, so building the dummy through `password.Hash`
keeps the two in step by construction.
`TestTimingDummyHashMatchesClientSecretParams` pins it.

The hash is kept per package rather than shared with `users` so each
package's equalizer string and failure log stay local; the cost is a second
argon2id hash at first use.

### D2 — a failed dummy build fails every Verify closed

As in `users` (#1913): when `password.Hash` fails the dummy is `""`, and
`Verify` returns `errTimingEqualizerUnavailable` before any lookup. All
callers map any `Verify` error to the same 401, so every client_id answers
alike.

### D3 — one decision function, one verify per path

`Verify` splits into `lookupForVerify` (DB) and `authenticate` (pure).
`authenticate` swaps in the dummy hash for an unknown, empty or disabled
client and always calls the verifier once. Revoked clients are the disabled
ones (`disabled_at` set); there is no other revoke state on
`oauth_clients`. A malformed client_id is just a string that matches no row,
so it takes the unknown path. An empty secret is no longer an early return:
it is verified like any other and rejected. Every rejection returns
`ErrUnknownClient`.

The verifier is an injected `secretVerifier` interface so the test counts
calls instead of measuring wall-clock time.

### D4 — a corrupt stored hash burns the dummy too

`password.Verify` rejects an undecodable stored hash before the argon2id
work, so that path calls the verifier a second time against the dummy hash.
The verifier runs twice there, but argon2id runs once, which is the cost
that matters. Only operator-side corruption reaches this path.

### D5 — the DB lookup itself is not equalised

A found row and a missing row still differ by the cost of the index lookup
and row scan. That is microseconds against an argon2id verify of tens of
milliseconds, well inside network jitter, and the #2221 rate limits bound
probing. Equalising it (for example a dummy query on miss) is not worth the
complexity. An empty client_id skips the query entirely, which is visible
only for the empty string, a value that names no client.
