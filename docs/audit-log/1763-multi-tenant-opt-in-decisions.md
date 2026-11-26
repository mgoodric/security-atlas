# #1763 — Multi-tenant opt-in and the tenant-count signal: decisions log

detection_tier_actual: none
detection_tier_target: none

## D1 — The gate counts tenants, it does not track a mode

`POST /v1/admin/tenants` refuses with `403` when at least one tenant
exists and `ATLAS_ALLOW_MULTI_TENANT` is not exactly `true`. "A second
tenant" is read as "any tenant beyond the first", counted the way
`platform.Status.TenantCount` counts (the all-zeros row excluded, #1885).
Nothing is stored: ADR 0023 rejects a tenancy-mode flag, and a count keeps
the opt-in one-way without a second source of truth. The super_admin check
runs first, so a non-super-admin still gets the existing `super_admin
required` error whatever the setting.

The check runs before the create transaction, not inside it. A race only
matters on a zero-tenant install, and a super_admin JWT always carries a
session tenant, so the admin API never runs there.

## D2 — The env var is read per request, like ATLAS_ENABLE_DEMO_SEED

`admintenants.DefaultAllowMultiTenant` reads the environment on each call,
with the same exact-`"true"` rule as the demo-seed gate, so `1` / `yes` /
`TRUE` stay off. `WithAllowMultiTenant` overrides it for tests. The
existing admintenants integration fixture opts in through that override,
because every test there seeds a session tenant first, so every create is
a second tenant. The new gate tests read the real environment via
`t.Setenv`.

## D3 — Tenant-count signal at GET /v1/me/tenancy

`{"tenant_count": n, "multi_tenant": n >= 2}`. A new endpoint rather than a
field on `GET /v1/me/tenants`: that endpoint lists the caller's
memberships from the JWT claim and is pinned to no table scan (P0-192-2),
while chrome needs the install-wide count. `GET /v1/install-state` already
carries `single_tenant` publicly; the authenticated endpoint carries the
count itself, which the public one deliberately does not.

## D4 — A path-pinned authz admit for every role

No rule admitted viewer or control_owner to `me` reads, so they could not
read the signal. `defaults.rego` gains one rule admitting a read of exactly
`/v1/me/tenancy` for any credential with a role. It does not widen other
`/v1/me/*` endpoints; `TestTenancy1763_NarrowAdmit` pins that.

## D5 — No fixture outside admintenants needed the opt-in

The RLS integration tests, the tenant-switch / tenant-name-after-switch
e2e fixtures (`fixtures/e2e/*.sql`), the ISA probes and the demo seed
insert tenants with SQL, not through the admin API, so the gate does not
apply to them. The admin-tenants and admin-users e2e specs mock
`/api/admin/tenants`. No fixture sets the variable for them, since none
would read it. Demo seed creating a separate tenant is #1765's to change.
