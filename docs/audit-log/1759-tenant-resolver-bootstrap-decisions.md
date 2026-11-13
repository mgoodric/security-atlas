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

## D3 — Serialization uses a transaction-scoped advisory lock

Attaching to an existing tenant inserts no `tenants` row, so the
`idx_tenants_bootstrap_singleton` index no longer serializes concurrent first
logins. `pg_advisory_xact_lock(hashtext('bootstrap_first_install'))` does that
now. The 23505 retry stays as the second leg of defense for the
create-tenant branch.

## D4 — Several tenants and no user: refuse, and answer 409

No tenant is implied, so the first user, who would also be super-admin, can't
be placed. The store returns the wrapped `*AmbiguousTenantError` and the OIDC
callback answers 409 with the tenant list. The URL's `tenant_id` is not used
here: on the bootstrap branch it has never been trusted.

## D5 — Upgrade migration: new `granted_via` value `single_tenant_upgrade`

The seed reuses `bootstrap_first_install`. An upgrade grant is a different
event, though, so the CHECK is extended (superset) and a new value records
where the grant came from. The bootstrap admin is the oldest active user with
role `admin` in the single tenant. An existing grant is left as it is
(`ON CONFLICT DO NOTHING`). With zero or several tenants the migration inserts
nothing. No `me_audit_log` row is written: no actor exists, and the action
CHECK would need its own extension. The `super_admins` row carries
`granted_via` and `granted_at`.

## D6 — Tests are store-level integration tests

The OIDC callback needs a live IdP for the code exchange. The acceptance tests
therefore drive `users.Store.BootstrapFirstInstallOrUpsert`, the
handler's only bootstrap dependency, against real Postgres in
`internal/auth/users/bootstrap_single_tenant_integration_test.go`. The 409
mapping in the handler is a two-line `errors.As`.
