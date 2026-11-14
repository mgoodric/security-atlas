# Tenant membership and switching

Most installs serve one organisation, and that is the default: a
**single-tenant install**. It has exactly one tenant, the bootstrap
tenant seeded from `ATLAS_BOOTSTRAP_TENANT`, and nobody types or sees a
tenant ID. Sign-in, the CLI, OAuth clients and the demo seed all resolve
to that tenant on their own, and the UI calls it "your organisation".
Nothing on the rest of this page applies until you opt in to
multi-tenant ([ADR 0023](https://github.com/mgoodric/security-atlas/blob/main/docs/adr/0023-single-tenant-experience-by-default.md)).

security-atlas also supports operators who belong to multiple tenants —
the canonical example is a vCISO consultant who hosts the platform
for several client tenants and needs to switch between client
contexts without signing out and back in.

This page describes:

- How to opt in to multi-tenant, and what changes when you do.
- How tenant membership is represented in atlas.
- How an operator switches tenants from the UI.
- What happens when an admin removes an operator from a tenant
  (the "eventual eviction" contract).
- How admins force-evict a removed operator immediately.

## Opting in to multi-tenant

An install becomes multi-tenant when a second tenant is created. That
takes two things together:

1. The deployment setting `ATLAS_ALLOW_MULTI_TENANT=true` on the `atlas`
   service (the exact lowercase string; restart after setting it). See the
   [configuration reference](configuration.md).
2. A super-admin creating the tenant, through `POST /v1/admin/tenants` or
   the admin Tenants page at `/admin/tenants`. On a single-tenant install
   the bootstrap admin is the super-admin.

Without the setting, `POST /v1/admin/tenants` answers `403` and the
install stays single-tenant. A tenant admin who is not a super-admin
gets `403` either way.

<!-- prettier-ignore-start -->
!!! warning "Multi-tenant is one-way"

    There is no supported path back from several tenants to one: tenants
    are not merged, and the install keeps behaving as multi-tenant while
    several exist. Turning
    `ATLAS_ALLOW_MULTI_TENANT` off later only stops further tenants from
    being created. If you only want to try the demo dataset, seed it into
    your organisation on a fresh install, or use a separate install
    (`docs/getting-started/demo-seed.md`).
<!-- prettier-ignore-end -->

Once a second tenant exists:

- The UI shows the tenant chrome: the tenant switcher, the Tenants and
  Super admins admin pages, and a tenant picker when assigning users.
- `/login` asks which tenant to sign in to, and
  `POST /auth/local/login` requires `tenant_id` (`400` without it).
- An OIDC sign-in link needs `tenant_id`
  ([OIDC setup](oidc-setup.md#step-4-sign-in-through-the-idp)).
- CLI commands that connect straight to Postgres need their tenant flag;
  without it they list every tenant and stop ([CLI](cli.md#tenant-flags)).
- New OAuth clients need `--tenant`, and clients issued before the
  upgrade that bound clients are not bound for you; bind each with
  `atlas-cli oauth bind-client` ([CLI](cli.md#oauth)).
- The demo seed creates its own tenant with `--tenant-slug` instead of
  loading into your organisation.

Row-level security is the same in both: every tenant's data is isolated
at the database layer whether one tenant exists or many.

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
hide the chrome when there's only one tenant. ADR 0023 completes it:
the install's tenant count, not a mode flag, decides which chrome
shows.)

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

## API keys the operator minted

API keys an admin mints (`POST /v1/admin/credentials`, or the
**Admin → API keys** page) are machine keys for connectors and CI.
They do not belong to the admin who minted them, so revoking that
admin's roles or disabling the admin leaves them working. Atlas
records the minter for attribution only.

The **Revoke roles** dialog on **Admin → Users** lists the keys the
user minted that are still live, with each key's scope, last use and
expiry. Nothing in that list is revoked automatically. Revoke a key
with its own **Revoke** button, or use **Revoke all**; both ask for
confirmation first. The list covers your current tenant; for a user
in another tenant, review that tenant's API keys page.

The same list is available from the API (admin only):

```bash
curl -H "Authorization: Bearer <admin-token>" \
  "https://<atlas-instance>/v1/admin/credentials?minted_by=<user-uuid>"
```

It returns the live keys (not revoked, not expired) that user minted
in the caller's tenant. Revoke one with
`POST /v1/admin/credentials/<id>/revoke` or
`atlas-cli credentials revoke --id <id>`.

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
