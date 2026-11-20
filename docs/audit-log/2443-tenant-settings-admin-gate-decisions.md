# #2443 — who may change the organisation's settings — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2443
**Date:** 2026-11-19

`PATCH /v1/tenants/{id}` (name, bundle gate mode, risk review cadence,
`allow_self_approval`, vendor initial review days) admitted a tenant admin,
while Settings › Organisation rendered only for a super-admin (#2216 D2). A
tenant admin could turn on self-approval through the API that the UI hid.

**Detection-tier classification (slice 353 / Q-13):**

- `detection_tier_actual`: manual_review (the review of #2437).
- `detection_tier_target`: integration. `TestPatchTenant2443_*` runs the
  real handler on Postgres with the credential authzmw would produce.

## D1 — organisation admins keep the whole endpoint (Matt, 2026-11-19)

The ticket asked whether to narrow the endpoint to super-admin or keep
tenant-admin access and record why. Matt's call: **organisation admins**,
`allow_self_approval` included.

- #2503 (2026-11-17) decided that organisation admins manage their own
  organisation's admin pages. That supersedes #2216 D2 for settings scoped
  to one tenant, and these are such settings.
- `allow_self_approval` weakens separation of duties, but a tenant admin
  already assigns roles inside their own tenant
  (`PATCH /v1/admin/users/{id}/roles`, #2503). In-tenant separation of
  duties is already in their hands; a super-admin gate on this one field
  would not be a real control.
- The primary user is a solo security leader, the person who needs
  self-approval. A super-admin-only gate would block them unless they also
  held the platform bit.

The change is audited either way: each `allow_self_approval` change writes a
`me_audit_log` row with before and after (#2101).

## D2 — the gate is `authz.IsTenantAdmin`, not the JWT role claim

The handler's own `callerCanRename` admitted `admin` in the JWT
`atlas:roles` claim. That claim outlives a role revoke until the token
expires (#2381), which is why #2503's handler gates read the live roles
authzmw resolves (`cred.TenantRoles`) through `authz.IsTenantAdmin`.
`PATCH /v1/tenants/{id}` now uses the same function, so the API and the
Settings page (`is_tenant_admin`, also `IsTenantAdmin`) admit the same
callers. A super-admin JWT and an admin-flagged key carry `cred.IsAdmin`
and are unchanged. A session whose admin role was revoked is now refused at
once (`TestPatchTenant2443_StaleAdminClaimForbidden`, which fails on the
old gate).

## D3 — Settings › Organisation renders for `is_tenant_admin`

`TenantSection` renders on the page's existing `canAdminister`
(`is_admin` or `/api/admin/me` `is_tenant_admin`), the same value that
already gates the Admin cross-link and the API tokens section. Hiding it is
UX only; the handler gate is the guard.

## D4 — API keys help wording

`help/api-keys` said a person-bound key "uses that person's current roles"
and "loses its access" on revoke. After #2427 that is only true for viewer-
and auditor-labelled bound keys; an empty-label bound key still derives
grc_engineer (#2431, #2401). Reworded to the ticket's text.
