# Decisions log — #1759: tenant resolver; bootstrap tenant is canonical

- detection_tier_actual: none
- detection_tier_target: none

Ticket: security-atlas#1759 (spec #1758, single-tenant install as the default
experience). The calls below were made while building it; none blocks merge.

## D1 — Resolver lives in `internal/tenancy` as `ResolveImplicitTenant`

`tenancy` already owns "which tenant is this?" for requests (`WithTenant`,
`TenantFromContext`), so the implicit-tenant question sits beside it. The
function takes a `Querier` (pool, conn or tx) so a caller can ask inside its
own transaction, as the OIDC bootstrap does. Results: one tenant →
`TenantRef{ID, Slug, Name}`; none → `ErrNoTenants`; several →
`*AmbiguousTenantError` listing ID, slug and name. The query excludes the
all-zeros tenant, so it is never returned. `tenants` is under FORCE RLS and the
question is asked before a tenant is known, so callers pass a BYPASSRLS
connection; the doc comment says so.

`ErrNoTenants` (plural) because `tenancy.ErrNoTenant` already means "no tenant
in context".

## D2 — The first-login gate is "no user exists", not "no tenant exists"

Before, the OIDC bootstrap ran only when `tenants` was empty. A Docker install
seeds the bootstrap tenant first, and that rule split first-login and seed
behaviour into two separate "default" paths. The gate is now "no user yet":
the first user is the bootstrap admin, and the resolver decides the tenant. If
a tenant exists, the user attaches to it, and that tenant is flagged
`is_bootstrap_tenant` when no tenant is flagged yet. With no tenant, the
Default Tenant is created and flagged as before.

## D3 — Serialization: fixed advisory lock under READ COMMITTED

Attaching to an existing tenant inserts no `tenants` row, so the
`idx_tenants_bootstrap_singleton` index no longer serializes concurrent first
logins. `pg_advisory_xact_lock(0x198198198198)` does that now. The key is a
fixed, documented BIGINT constant (`bootstrapAdvisoryLockKey`), following
slice 142's `0x142142142142`; `0x198` names slice 198, which introduced the
bootstrap path. The transaction sets `IsoLevel: pgx.ReadCommitted`
explicitly. That setting matters: each statement takes a fresh snapshot, so
the gate, read after the lock is granted, sees a user the winner committed
while this transaction waited. The 23505 retry stays as the second leg of
defense for the create-tenant branch. A concurrent test against a seeded
tenant checks that exactly one first login wins.

## D4 — Several tenants and no user: refuse with a generic 409

No tenant is implied, so the first user, who would also be super-admin, can't
be placed. The store returns the wrapped `*AmbiguousTenantError`. Reaching the
callback takes only an IdP login, so on this path the handler:

- logs the tenant list server-side (`slog.WarnContext`);
- clears the OIDC flow cookies;
- answers a generic 409 (`ErrMsgBootstrapAmbiguousTenant`) that names no
  tenant UUID, slug or name.

This follows spec story 6, no tenant identifiers on public surfaces; the list
is for operators (CLI story 16). The URL's `tenant_id` is not used here: on
the bootstrap branch it has never been trusted.

## D5 — Upgrade migration grants only when no super-admin exists

The seed reuses `bootstrap_first_install`. An upgrade grant is a different
event, though, so the CHECK is extended (superset) and the new value
`single_tenant_upgrade` records where the grant came from.

The migration grants only when **no `super_admins` row exists at all**. "If
missing" means the install has no super-admin, not that one particular user
lacks the grant. In each of these cases a grant would be one nobody chose:

- the operator moved super-admin to another user;
- the bootstrap admin was deactivated but still holds the grant;
- group-derived admins exist.

When the migration does grant, it chooses the bootstrap admin in this order:

1. an identified bootstrap user who is still an active admin: the Docker
   seed's well-known id (`c0000000-0000-4000-8000-000000000001`) or the user
   on a `bootstrap_first_install` `me_audit_log` row;
2. otherwise, the oldest active admin.

With zero or several tenants it grants nothing. A `RAISE NOTICE` names the
grantee, or says why nothing was granted. No `me_audit_log` row is written: no
actor exists, and the action CHECK would need its own extension. The
`super_admins` row carries `granted_via` and `granted_at`.

## D6 — Tests at two layers: handler and store

An earlier revision claimed the OIDC callback needs a live IdP. That was
wrong: an in-process `httptest` IdP (discovery, JWKS, and a token endpoint
that mints a nonce'd ID token) drives the real code exchange, as
`internal/auth/oidc/oidc_nonce_integration_test.go` already does. The
acceptance criteria are now covered at the API layer in
`internal/api/auth/bootstrap_integration_test.go`, with real Postgres:

- **AC1:** a seeded bootstrap tenant gets a 302 with a session cookie; the
  user and the session are in that tenant; exactly one tenant remains.
- **AC3:** two tenants with no users get a 409; nothing is written, the flow
  cookies are cleared, and the body carries no tenant UUID, slug or name.
  The leak assertion was checked by putting the tenant list back into the
  body and watching the test fail.

`./internal/api/auth/...` is enrolled in integration shard B1. The store
tests in `internal/auth/users/bootstrap_single_tenant_integration_test.go`
remain as the lower layer, along with the migration tests.

## D7 — The first-login gate also requires no super_admins row

`super_admins` has no FK to `users`, so a grant outlives its user. Gating on
"no users" alone would reopen bootstrap on an install whose users were all
deleted, and the next IdP login would receive admin plus super_admin. The
gate is now "no user AND no super_admin". A test covers the
users-deleted case.
