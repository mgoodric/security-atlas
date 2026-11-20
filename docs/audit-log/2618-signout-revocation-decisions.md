# 2618 — Bulk evaluate retries, idempotent exception requests, plan picker search, sign-out revocation: decisions log

Issue: mgoodric/security-atlas#2618 (follow-ups from the review of #2617 /
#2141, plus one from #2399).

## Detection-tier classification

- **detection_tier_actual:** unit. The first draft of the Idempotency-Key
  lock used the single-bigint `pg_advisory_xact_lock`; the
  `TestNoUnregisteredAdvisoryLocks` guard caught it before review.
- **detection_tier_target:** unit.

## Decisions

### D1 — sign-out revokes the session JWT through the existing self-revocation path

`signOut` deleted the cookie and nothing else, so a copy of the session JWT
stayed valid until it expired. Revocation infrastructure already existed:
the slice-190 jti denylist (`internal/auth/revocation`), checked on every
request by `internal/auth/jwtmw`, and `POST /oauth/revoke`, which accepts
RFC 7009 self-revocation (the token presented as its own bearer). Before it
deletes the cookie, `signOut` now posts `token=<cookie JWT>` to
`/oauth/revoke` with that JWT as the bearer.

Considered and rejected:

| Option                        | Why not                                                                                   |
| ----------------------------- | ----------------------------------------------------------------------------------------- |
| A new session table           | A second revocation mechanism beside the jti denylist; nothing it does that one cannot.   |
| A new BFF logout route        | `signOut` is a server action that already holds the cookie; one more hop adds nothing.    |
| Revoke only on "all sessions" | The copied-cookie risk applies to every sign-out, not only the explicit "everywhere" one. |

The revoke is **best effort**. A network failure or a non-2xx response is
logged server-side (`console.error`), and the cookie is still deleted and
the redirect still happens. Failing the sign-out would strand the user
signed in, which is worse than the residual risk of one unrevoked token
until its expiry (8h cookie lifetime). The call forwards the user's IP as
`X-Forwarded-For`, because the revoke endpoint throttles per client IP
(#2041), and without it every sign-out would share the web container's
bucket.

A pasted non-JWT bearer (an API key used through the developer-token card)
fails `Verify` and gets RFC 7009's silent 200. Signing out never revokes an
API key; key revocation stays an admin action.

There is no schema change. This only tightens: every token it revokes was
already revocable by its own holder.

### D2 — Idempotency-Key lives in its own table, keyed per requester, with a 24h window

`POST /v1/exceptions` accepts `Idempotency-Key` (1 to 255 printable ASCII).
The key, a sha256 of the canonical request, and the exception it created go
in `exception_idempotency_keys`, primary key `(tenant_id, requested_by,
idempotency_key)`:

- **Per requester.** Two people who happen to send one key file two
  exceptions. A key never replays someone else's request.
- **Window 24h** (`exception.IdempotencyWindow`). A retry after a timeout
  lands well inside it. A key reused after it files a new exception and
  overwrites the row.
- **Same key, different body: 422.** That is a client bug, not a retry. The
  hash covers control, compacted predicate, justification, compensating
  controls and the expiry normalised to UTC, so reformatting one request
  does not count as a change.
- **Replay: 201 with the first exception and `Idempotent-Replayed: true`.**
  Same status as the first send, so a client needs no second success path.
- **Concurrency:** a transaction-scoped advisory lock (namespace 12,
  `advisorylock.ExceptionIdempotencyKey`) serialises one key, so a
  concurrent duplicate waits and then replays. The integration test sends
  five at once and gets one exception.

The key is a separate table rather than columns on `exceptions` because it
is request plumbing, not part of the exception, and is forgotten after the
window.

The request form keeps the last key it sent with the body it sent it for.
Resubmitting an unchanged form reuses the key, and an edited form gets a
fresh one. A bulk request sends `<key>:<control id>` per control, so a
partial retry replays the controls that went through and files the rest.

### D3 — bulk evaluate waits out Retry-After, batch-wide, and can be cancelled

Evaluation is limited per principal (`ATLAS_EVALUATE_RATE_PER_MIN`, default
6), so a 20-control bulk evaluate used to get 14 refusals. The 429 already
carried `Retry-After`, but the BFF's `forwardJSON` dropped every upstream
header except Content-Type, and now passes `Retry-After` through.

`runPerItem` retries a 429 that names a Retry-After. The pause is batch-wide
(`pausedUntil`), not per worker: the limit is per principal, so four
workers each sleeping on their own would just collide again. An item gives
up after 10 tries (`MAX_RATE_LIMIT_ATTEMPTS`), or at once when the wait
exceeds 120s (`MAX_RETRY_AFTER_SECONDS`), and is then refused as
rate-limited.

While it runs the selection bar shows "n of N evaluated" and a Cancel
button. Cancelling refuses as cancelled every item not yet started and any
item waiting out a Retry-After. A write already in flight finishes, since
evaluation runs server-side either way.

The result collapses rate-limited and cancelled refusals into one line each
("14 controls not evaluated: rate limit reached, retry in ~10s"). The rate
itself is not in the line: it is server configuration the client does not
know.

### D4 — plan picker: server-side title search, a page at a time

`GET /v1/action-plans` takes `?q=` (at most 200 characters), matching
titles that contain it, case-insensitively. The match is
`strpos(lower(title), lower(q))` rather than ILIKE, so `%` and `_` in a
search match literally without an escaping helper. The picker debounces the
search box (250ms, the shared `useDebounced` hook moved to `web/lib` from
the entity picker), loads 25 plans per page, and offers "Load more plans"
while the server returns a `next_cursor`.
