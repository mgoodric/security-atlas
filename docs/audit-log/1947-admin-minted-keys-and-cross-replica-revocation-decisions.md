# Decisions log — #1947: admin-minted keys outlive their minter; revocation holds on every replica

- detection_tier_actual: manual_review
- detection_tier_target: integration

Ticket: security-atlas#1947, from the #1945 review (#1915). The ADR 0003 #1947
amendment records the outcome. This log records the calls made along the way.

## D1 — Admin-minted keys stay independent of the minter (Matt's call)

Two models were on the table: (A) record the minter for attribution and keep
the key live, or (B) revoke the minter's keys when the minter is disabled.
Matt chose A. These are connector and CI push keys, and under B an admin's
offboarding would silently stop evidence ingestion. The admin-UI warning
that lists a user's still-live minted keys is a separate ticket.

## D2 — The minter goes in `minted_by`, never `issued_by`

The ticket offered either column. `issued_by` is read as the key's owner:
`credentialFromRow` maps it to `UserID`, and the user gate refuses on the
owner's status. Writing the minter there would make these keys die with the
admin, which is model B. `minted_by` is the #1892 attribution-only column.

## D3 — Share the minter derivation in `credstore`

`minterUserID` lived in `internal/api` (the reset-bootstrap handler).
`internal/api/admin` and `internal/api/admincreds` cannot import that
package, so it moved to `credstore.MinterUserID`. It also returns "" when
the caller's UserID is the caller's own id, matching `OwnedByUser`'s case
for a uuid-form credential id.

## D4 — Check `revoked_at` in the existing gate, not with a sync channel

The ticket offered a `revoked_at` check, a revocation generation, or
cross-replica sync. The gate already does one database read per
authentication of a user-owned key, so a primary-key read of the key's own
row is the smallest change, and it needs no new infrastructure. It uses the
existing `GetAPIKeyByID` query, so there is no migration and no sqlc
regeneration.

## D5 — The gate gets a key id only for persisted keys; a missing row refuses

A memory-only credential has no row, so the credstore passes an empty key id
and only the owner check runs. A persisted key whose row is gone is refused.
That fails closed, matching how a missing `users` row already refuses.

## D6 — Scope is user-owned keys

Machine keys still skip the gate. A machine key revoked on one replica stays
live in other replicas' memory until restart. That is a pre-existing gap,
outside this ticket, and closing it would put a database read on every push
authentication.

## D7 — The apikeystore Rotate successor inherits `minted_by`

The credstore Rotate already carried `MintedBy` to the successor (#1892).
The DB-backed Rotate did not. It does now, so rotating a key keeps its
attribution.
