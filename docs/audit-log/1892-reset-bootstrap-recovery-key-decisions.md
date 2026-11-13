# Decisions log — #1892: the reset-bootstrap recovery key is bounded, marked and traceable

- detection_tier_actual: manual_review
- detection_tier_target: unit

Ticket: security-atlas#1892, from the #1886 review (#1885). Since #1886,
`POST /v1/admin/install/reset-bootstrap` mints a tenant admin key. It had the
right privilege but no expiry, no record of who minted it, and it outlived
the next reset. The ADR 0003 #1892 amendment records the outcome; this log
records the calls made on the way.

## D1 — 24h default TTL, 7d cap, no non-expiring recovery key

The bearer-paste sign-in sets an 8h session cookie, so the default has to
exceed 8h or the recovered session dies early. 24h covers that and means a
forgotten key is dead the next day. The 7d cap covers a recovery that waits
over a weekend. `ttl_seconds` 0 or absent means the default; negative or
above 7d is a 400. `IssueRecoveryAdmin` refuses `ttl <= 0`, so the store
cannot mint a non-expiring recovery key even if the handler is bypassed.

## D2 — Enforce TTL in `credstore.Authenticate` for every key

Without it the bound in D1 holds only across a restart: the in-memory store
applied TTL by not reloading expired rows at boot. Checked callers before
changing it. Every in-tree issue path passes `ttl=0` (bootstrap, fixed
admin, approver, owner), the chaos and CI scripts pass `--ttl 0`, and OAuth
clients are a separate store. The DB-backed `apikeystore.Store` already
rejects past `expires_at`, so a key past its TTL was already refused on that
path. What changes is every key with a non-zero TTL that lives in the
in-memory store: one issued via the admin gRPC `Issue` RPC with `--ttl`,
and one issued via `POST /v1/admin/credentials` with `ttl_seconds`, which
the boot scan (`LoadActive`) loads from `api_keys`. Each now expires on
time instead of authenticating past its expiry until the next restart.
That is what both TTL options document. `TestAuthenticate_TTLZeroNeverExpires`
pins `ttl=0`.

The handler range-checks `ttl_seconds` before converting it to a
`time.Duration`. A large value would otherwise wrap negative, pass a check
on the product, and fail only at the mint, after the status reset (500
instead of 400). Found in review; `TestResetBootstrap_RecoveryKeyTTL` covers
2^62 and max int64.

## D3 — Persist the recovery marker as `api_keys.purpose`

The first draft tracked reset-minted ids in server memory. The dispatcher
(relaying for Matt) asked for a durable marker, because a restart between
the reset and the sign-in would lose the id. A nullable `purpose` column
with a one-value CHECK (`'bootstrap-recovery'`) is the smallest durable
marker. It is not tenant-scoped data, so it needs no RLS change. A Rotate
successor inherits it, so rotating a recovery key cannot move it out of
reach of revocation.

## D4 — Sign-in revokes recovery keys other than the caller's own

The ticket asked `mark-first-signin` to revoke the key whose bearer it
deletes from the file. Its only caller is the bearer-paste `signIn` action,
which uses the pasted key as the session. After a reset that key is
normally the recovery key, so revoking it would sign the operator straight
back out. Resolved with the dispatcher: revoke every recovery key except
the caller's (by marker and id, never by reading the bearer from the file),
keep the caller's key until its TTL, and say which happened in the response
and the log. The docs tell the operator to revoke it once they can sign in
another way.

## D5 — The minter goes in `api_keys.minted_by`, not `issued_by`

The ticket asked to set `api_keys.issued_by`. The first draft did that by
setting the recovery key's `UserID` to the minting user, which the Persister
writes as `issued_by`. Review (relayed by the dispatcher) asked whether
`UserID` means more than attribution. It does, and so does `issued_by`:

- `credentialFromRow` maps `issued_by` to the key's `UserID`.
- `/v1/me` and approval records (`approver_user_id`) resolve `UserID`.
- The policy-acknowledgment denominators (`CountRequiredRoleUsersForVersion`,
  the policies list) count distinct `issued_by` over admin keys as users who
  must acknowledge.
- The admin audit log reads `issued_by` as the key's user.

A recovery key written that way would act as, and count as a key of, the
operator who minted it. So the minter goes in a dedicated nullable column,
`api_keys.minted_by` (same migration as `purpose`), carried on the
credential as `MintedBy`. Nothing reads it for identity or authorization. A
recovery key keeps `issued_by` NULL and `UserID` equal to its own
credential id, before and after a restart.

JWT subjects are `user:<uuid>`; the prefix is stripped. A caller with no
user (an API key) records no minter, and the log names its credential id
instead.

## D6 — Logging, not an audit-table row

There is no admin audit-event sink on the server. The OPA decision audit
already records the request. The mint is logged at Info with the new
credential id, tenant, expiry, caller credential id and subject, and the
ids it revoked. The bearer is never logged, in whole or in part.
