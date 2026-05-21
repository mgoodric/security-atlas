# Tenant membership and switching

security-atlas supports operators who belong to multiple tenants —
the canonical example is a vCISO consultant who hosts the platform
for several client tenants and needs to switch between client
contexts without signing out and back in.

This page describes:

- How tenant membership is represented in atlas.
- How an operator switches tenants from the UI.
- What happens when an admin removes an operator from a tenant
  (the "eventual eviction" contract).
- How admins force-evict a removed operator immediately.

## How membership is represented

Atlas binds an operator to a tenant by inserting a row in the
`users` table scoped to that tenant. An OIDC subject (identified
by `idp_issuer` + `idp_subject`) MAY have a `users` row in many
tenants — one per tenant they belong to.

When the operator signs in via OIDC, atlas's OAuth Authorization
Server issues a JWT carrying:

- `atlas:current_tenant_id` — the tenant scope of THIS session
- `atlas:available_tenants[]` — the full set of tenants the
  operator can switch among (every tenant they have a `users`
  row in, intersected with `status = 'active'`)
- `atlas:roles` — a tenant-keyed map of role lists
- `atlas:super_admin` — global escalation flag (rarely set)

The frontend tenant-switcher dropdown reads
`atlas:available_tenants[]` via the `GET /v1/me/tenants`
endpoint and shows one row per tenant.

## How to switch tenants

If the operator's account is in **only one tenant**, the
switcher chrome is hidden — there's nothing to switch to. The UI
operates against the single tenant directly. (Canvas §11 item 13
documents this commitment: build multi-tenant from day one, but
hide the chrome when there's only one tenant.)

If the operator's account is in **two or more tenants**, the
header includes a persistent dropdown showing the current
tenant's name + a chevron. Clicking the dropdown opens a list of
all available tenants; clicking a non-current tenant triggers a
**tenant switch**:

1. The browser calls the BFF route `/api/auth/switch-tenant`
   with the target tenant's UUID.
2. The BFF calls the platform's `POST /oauth/token` with
   `grant_type=urn:ietf:params:oauth:grant-type:token-exchange`,
   passing the current JWT as `subject_token` and the target
   tenant as `atlas:target_tenant_id`.
3. The platform validates the target tenant is in the current
   JWT's `atlas:available_tenants[]` (or that the operator is
   `super_admin`), then mints a new JWT scoped to the target
   tenant.
4. The BFF replaces the `atlas_jwt` cookie with the new token.
5. The frontend calls `router.refresh()` so all server
   components re-render against the new tenant scope.

The URL does NOT change. Tenant scope is carried entirely by
the JWT cookie. There's no `/tenant-acme/dashboard` path —
that's a v3 design decision that may revisit per-tenant URL
routing.

## Eventual eviction

When an admin removes an operator from a tenant (by deleting
the operator's `users` row in that tenant, or setting `status`
to `disabled`), the operator's **existing tokens still work
until they expire**. This is the OAuth standard "eventual
eviction" semantic, not a bug.

What this means in practice:

- An operator with a 1-hour JWT issued at 14:00 can still
  access the tenant they were just removed from until 15:00,
  when the JWT expires and they cannot re-acquire one.
- The operator's NEXT call to `GET /v1/me/tenants` (every 60
  seconds while the tab is open) will return a tenant list
  that no longer includes the removed tenant. The frontend
  surfaces this as a yellow banner: "Your access to the
  current tenant was removed. Switch to another tenant or
  sign out."
- The default action on the banner switches the operator to
  the first available alternative tenant.

If the operator is in only one tenant and is removed from it,
the next refresh of `GET /v1/me/tenants` returns an empty list.
The frontend's switcher chrome remains hidden (single/zero
tenant rule), and any data-bearing page will start returning
401s as the JWT's claim becomes inconsistent with the operator's
actual `users` row presence.

## Forcing immediate eviction

If an admin needs to revoke an operator's access _immediately_
(not eventually-at-expiry), they call the OAuth revocation
endpoint:

```bash
curl -X POST https://<atlas-instance>/oauth/revoke \
  -u "<client_id>:<client_secret>" \
  -d "token=<jwt-to-revoke>" \
  -d "token_type_hint=access_token"
```

(Or via the operator's own self-revocation path with the JWT
in the `Authorization: Bearer` header.)

After revocation, the JWT validation middleware (slice 190)
rejects the token on the next request with `401`. The operator
must sign in again — and at that point the new JWT will reflect
the updated `available_tenants[]` (without the removed tenant).

## Why eventual?

Eventual eviction is the OAuth-standard contract for stateless
access tokens. The trade-off:

- **Stateless tokens** = no DB lookup per request → fast,
  scalable, cache-friendly.
- **Eventual eviction** = membership changes propagate at the
  cadence of token expiry (default 1 hour) OR via explicit
  revocation.

Atlas chose stateless tokens with the `/oauth/revoke` escape
hatch over the alternative — per-request session lookup with
synchronous invalidation — because the OAuth model:

- Is well-understood by security reviewers.
- Has a documented force-revoke path (RFC 7009).
- Carries an authoritative audit trail
  (`oauth_token_exchanges` for switches, `oauth_revoked_tokens`
  for revocations).
- Composes with future client capabilities (refresh-token
  grants, DPoP, mTLS — all v3 deferred but architecturally
  compatible).

If an admin removes a user from a tenant and needs that user's
access to drop immediately rather than within an hour, the
`/oauth/revoke` endpoint is the supported path.
