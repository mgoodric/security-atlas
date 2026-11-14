# #1913 — Login throttling, id-only implicit-tenant query, dummy-hash failure — decisions log

Build-time calls made while closing #1913 (from the #1907 review of #1727).
This log does not block merge.

- detection_tier_actual: manual_review
- detection_tier_target: manual_review

(All three gaps came out of PR review of #1907. None reached production.)

## D1 — Two token buckets, both before argon2id

`POST /auth/local/login` checks two in-memory token buckets
(`internal/api/auth/login_throttle.go`) before it does any argon2id work:

| Bucket      | Key                                        | Burst | Refill | Checked                           |
| ----------- | ------------------------------------------ | ----- | ------ | --------------------------------- |
| Per IP      | `clientIP(r)` (honors TRUSTED_PROXY_CIDRS) | 60    | 60/min | first, before the body is decoded |
| Per account | tenant id + lowercased, trimmed email      | 10    | 5/min  | after tenant resolution           |

Either bucket answers `429` with `Retry-After`, which is the number of
whole seconds until the next token, and the body
`{"error":"too many login attempts"}`.

The account key comes from the request alone and never from a lookup.
An unknown email is throttled exactly like a known one, so a 429 is not
an account-existence oracle. Emails are lowercased because
`GetUserByEmail` matches case-insensitively; otherwise case variants of
one address would each get their own budget.

The limiter is in-memory and per process, the same trade-off as the
OAuth token endpoint's `tokenBucketLimiter`. Buckets that have refilled
to the burst are swept once the map passes 4096 keys, and the next
sweep waits until the map has doubled.

## D2 — Accepted risks

- **UI logins share one per-IP bucket.** The `/login` page signs in
  through a Next.js server action (`web/app/login/actions.ts`), which
  calls the API itself and forwards no client IP. Every browser login
  therefore reaches the API from the web container's address, so the
  per-IP bucket becomes a global ceiling for UI logins: 60 at once, then
  60/min. An attacker who drives the UI hard enough can deny UI sign-in
  to everyone until the bucket refills. Direct API callers are keyed on
  their own IP. The fix is #2040: the BFF forwards the client IP, and the
  web container's CIDR is documented for `TRUSTED_PROXY_CIDRS`.
- **Targeted account lockout.** Anyone who knows or guesses an email can
  drain that account's bucket and keep it locked for minutes at a time
  (5/min refill). That is the usual price of per-account throttling, and
  it is accepted here over unlimited guessing against one account.

## D3 — Other unauthenticated endpoints that do expensive work

Checked as the ticket asked:

- `/oauth/token` (client_credentials) is already throttled per
  `client_id` before its argon2id verify.
- `/oauth/introspect` and `/oauth/revoke` run an argon2id client-secret
  verify for any known `client_id` with no throttle. Out of scope here;
  filed as #2041.
- `/auth/oidc/login` and `/auth/oidc/callback` do no password hashing.

## D4 — Id-only implicit-tenant query

`tenancy.ResolveImplicitTenantID` runs
`SELECT id FROM tenants WHERE id <> $1 ORDER BY created_at, id LIMIT 2`.
`PoolImplicitTenantResolver` uses it for local login, OIDC login and
`/oauth/authorize`. `ResolveImplicitTenant`, which returns slug and name,
stays for the CLI, where the ambiguity error is meant for an operator.

## D5 — Dummy-hash build failure fails closed

If `password.Hash` fails while the timing-equalizer hash is being built,
the failure is logged with `slog.Error`. `VerifyLocalLogin` then refuses
every login with an internal error before any lookup, so the handler
answers 500. Refusing only unknown accounts would make a fast 500 against
a slow 401 the same existence oracle the equalizer exists to close.
