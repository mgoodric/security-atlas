# Decisions log — #1964: revocation holds on every replica for every persisted key

- detection_tier_actual: manual_review
- detection_tier_target: integration

Ticket: security-atlas#1964, from the #1952 review of #1947 (its decisions
log D6). The ADR 0003 #1964 amendment records the outcome. This log records
the calls made along the way.

## D1 — Read `api_keys.revoked_at` per request, uncached

The ticket offered a per-request read (optionally behind a TTL cache), a
per-tenant revocation generation, or LISTEN/NOTIFY. The read reuses
`GetAPIKeyByID`, the query #1947 already runs for user-owned keys, so there
is no migration and no sqlc regeneration. No cache: the bound on staleness
is the next request, and the user-owned path already pays the same read
uncached. A cache can be added later if a measurement shows the read
matters; it would widen the window in which a revoked admin key works.

## D2 — A second gate method, not an empty user id

`UserGate` gains `KeyCanAuthenticate(tenantID, keyID)`. Passing an empty
user id to `UserCanAuthenticate` would have worked, but a malformed user id
currently refuses, and making "" mean "no owner" would put two meanings on
one argument. Both methods share `keyLive` in `apikeystore`.

## D3 — Gate only persisted records; the bootstrap key is untouched

Authenticate gates a no-user credential only when its record is marked
`persisted` (rehydrated at boot, or issued or rotated while persistence was
attached). The env bootstrap key and `IssueFixedAdmin` credentials are
memory-only, have no row, and never reach the gate, so a database outage
cannot lock out the bootstrap path. A persisted key whose row is missing
refuses, matching #1947 D5.

## D4 — The recovery key is gated too

`IssueRecoveryAdmin` writes through to `api_keys` when persistence is
attached, so in production its record is persisted and it now reads its
row on each authentication like any other machine key. Revoking a recovery
key therefore also holds on every replica. Without persistence (unit
servers) it stays memory-only and ungated.
