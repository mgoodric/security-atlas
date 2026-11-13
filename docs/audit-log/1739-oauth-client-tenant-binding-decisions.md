# Decisions log — #1739: OAuth clients are bound to a tenant

- detection_tier_actual: production
- detection_tier_target: integration

Ticket: security-atlas#1739 (ticket 5 of spec #1758). A `client_credentials`
token was tenant-free by design, so `atlas-cli controls upload --client-id`
(which `bootstrap.sh` runs) wrote the stock controls into the all-zeros
tenant, where the signed-in user saw none of them. An adopter found it on a
first native install (2026-11-12). The `test-self-host-bundle.sh` smoke test
counted 50 rows in `controls` without asking which tenant held them; it now
asserts the tenant and the API count (assertion 8), and an integration test
drives the path from `/oauth/token` to `GET /v1/controls`.

## D1 — The binding lives on `oauth_clients`, not in the token request

A client carries `tenant_id` (NULL = unbound), and the token endpoint stamps
it as `atlas:current_tenant_id` with `available_tenants = [tenant]`
(`jwt.Validate` requires the current tenant to be available). The other
option the ticket named, RFC 8693 exchange per upload, would make every
machine caller do a second round-trip, and an unbound machine token has no
`available_tenants` to pass the exchange allowlist anyway. Roles stay empty
and `super_admin` stays false. Token exchange is unchanged.

`tenant_id` references `tenants(id) ON DELETE SET NULL`: deleting a tenant
leaves its clients unbound (their writes refused) instead of blocking the
delete. `oauth_clients` stays RLS-exempt; the token endpoint reads it before
any tenant is known.

## D2 — The refusal is in `tenancymw`, for every write, from any credential

"Any write whose credential resolves to the all-zeros tenant is refused." That
is the middleware that lifts the credential's tenant onto the request, so the
check sits there: a credential whose tenant parses to `uuid.Nil` gets
`403 {"error":"tenant_unbound","message":...}` on any method but
GET/HEAD/OPTIONS, before a handler runs. The message names
`atlas-cli oauth bind-client`. Reads pass, which is what spec story 14 asks
for ("unbound clients keep reading"); under the all-zeros tenant they return
nothing.

403 rather than 401: the token is valid; it cannot act here.

## D3 — `issue-client` binds by default; `--unbound` is the explicit opt-out

`issue-client` takes `--tenant`; without it, `tenancy.ResolveImplicitTenant`
picks the only tenant. Several tenants, or none visible, are refused with an
error that says to pass `--tenant`. They are never defaulted. Public clients
for the authorization-code and device flows (`cli-public`, the chaos probe)
need no tenant, because the user's token carries one, so `--unbound` issues
those. The resolver needs a BYPASSRLS connection. `bootstrap.sh` already uses
`atlas_migrate`, and an `atlas_app` caller passes `--tenant`; the help text
says so.

## D4 — `bind-client` refuses to move a bound client

Binding an unbound client, or re-binding it to the same tenant, succeeds.
Re-binding to a different tenant returns `ErrAlreadyBound`. Moving a client
would silently redirect every write its tokens make, so the remedy is a new
client. Because the same-tenant bind is idempotent, `bootstrap.sh` binds a
reused persisted client on every run. That upgrades a client persisted by an
older bootstrap, and fails loudly if it is bound elsewhere.

## D5 — `migrate-api-key` binds to the legacy key's tenant

The migrated OAuth client is bound to the source API key's tenant, so its
writes land where the key's did. Leaving it unbound would have made every
migrated client unable to write.

## D6 — Upgrade migration binds only on a single-tenant install

`20260811000000_oauth_client_tenant_binding.sql` binds every unbound client to
the tenant when exactly one tenant exists, not counting the all-zeros row. That
matches the #1759 super-admin upgrade's rule. With zero tenants or several it
binds nothing and raises a NOTICE that names `bind-client`.

## D7 — Existing all-zeros controls are left in place

Controls already written to the all-zeros tenant are not moved. Moving rows
across tenants is a data decision for the operator. The PR body gives the
read-only count query.
