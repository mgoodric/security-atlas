# #1762 — CLI tenant flags optional on single-tenant installs: decisions log

detection_tier_actual: none
detection_tier_target: none

## D1 — API-backed credentials commands default server-side

`credentials issue` and `credentials list` call the gRPC
`AdminCredentialsService`. The CLI cannot read its own bearer's tenant
without a new RPC, so the default lives in the server: an empty
`tenant_id` on `Issue` / `List` means the caller credential's tenant. A
caller with no tenant (empty, or the all-zeros id a super_admin JWT
carries outside any tenant) still gets `InvalidArgument: tenant_id is
required`. `--reset-bootstrap` already minted for the caller's tenant
(`install_reset.go`); the CLI now omits `tenant_id` from that body when
`--tenant` is not given.

## D2 — The database-backed commands resolve through atlas_service_account

`evidence verify` and `oscal-export` run on the `atlas_app` DSN, and
`policy seed-stock` documents that role too. `tenants` is under FORCE RLS,
so `tenancy.ResolveImplicitTenant` on that pool sees no tenant. Rather
than ask for a second DSN, `tenancy.ResolveImplicitTenantAnyRole` does the
read under `SET LOCAL ROLE atlas_service_account`, the BYPASSRLS read role
`atlas_app` is already granted (`migrations/bootstrap/01-roles.sql`) and
that `evidence verify --all-tenants` already uses. A BYPASSRLS session
(atlas_migrate) reads directly, so `oauth issue-client` / `bind-client`
behave as before.

## D3 — An implied tenant is named on stderr, by name

When the flag is omitted and one tenant exists, the command writes
`tenant: "<name>", the install's only tenant` to stderr. The operator
sees what they acted on without a UUID (ADR 0023), and stdout stays
machine-readable.

## D4 — demo `--tenant-slug` is left to #1765

`demo seed --tenant-slug` names a NEW tenant to create; with the flag
omitted, the single-tenant behaviour is "seed into the sole tenant when
it holds only stock data", which is #1765's acceptance criteria
(record the seeded rows, refuse on real data, teardown exactness).
Making the flag optional here without that would either seed a tenant
the guard refuses or invent a default slug that turns the install
multi-tenant, which #1758 forbids. This change leaves `demo` untouched.

## D5 — `internal/tenancy` enrolled in integration leg B5

The real-Postgres falsifier for D2 lives in
`internal/tenancy/resolve_integration_test.go`, since CI's integration
tier covers `./internal/...` only. It inserts and deletes only its own
tenants, so it is safe on any leg; B5 is the lightest.
