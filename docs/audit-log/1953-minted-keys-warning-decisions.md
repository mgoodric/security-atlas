# Decisions log — #1953: warn about a user's still-live minted API keys

- detection_tier_actual: none
- detection_tier_target: none

Ticket: security-atlas#1953, the follow-up named in the ADR 0003 #1947
amendment. Admin-minted keys outlive their minter (Matt, 2026-11-13); this
makes the surviving keys visible where an admin removes a user.

## D1 — A filter on the existing list, not a new endpoint

`GET /v1/admin/credentials` already lists the tenant's keys, admin-only,
under RLS. A `?minted_by=<uuid>` query parameter narrows it in the handler.
No new route, no new query, no migration. The list response gains
`minted_by` and `expires_at` for every caller, both of which are already
on the row and carry no secret.

## D2 — "Live" means not revoked and not past expiry

The store's list already drops revoked rows. The filter also drops keys
past `IssuedAt + TTL`. A rotated-out predecessor inside its grace window
still authenticates, so it is listed; `retires_at` is not carried on the
credential, and adding it was outside this ticket. Its successor inherits
`minted_by` (#1947 D7), so both appear until the grace window ends.

## D3 — The screen is the Admin → Users revoke dialog

The web UI has no separate disable-user action; disabling happens through
SCIM or the IdP. The revoke-roles dialog is the screen where an admin
removes a user, so the panel lives there.

## D4 — Never revoke automatically; confirm every revoke

The panel only lists. Revoking the user's roles sends no key revokes. A
key revoke takes a Revoke click and a Confirm click; "Revoke all" takes a
click and a confirmation that names the count. The Playwright spec asserts
no revoke call fires before each confirmation.

## D5 — Cross-tenant users get a pointer, not a list

The credentials API lists the caller's own tenant. For a super_admin
revoking a user in another tenant, an empty list would read as "no keys",
which may be false. The panel says the keys keep working and points to
that tenant's API keys page, and makes no credentials call.

## D6 — Keys have no name column

The ticket asks for a name. `api_keys` has none, so the panel shows the key
id and its last four characters, the same identity the API keys page uses.
