# #2216 — /v1/me roles and who /admin is for — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2216
**Date:** 2026-11-14

A password or OIDC sign-in mints a JWT with Subject `user:<uuid>`, which
becomes `cred.UserID`. `/v1/me`'s `resolveRoles` passed that raw string to
`user_roles`, which stores the bare uuid, so every signed-in person got
`"roles": []`. Separately, `/admin` is gated on `is_admin` (super-admin only)
while its denial copy blamed the `admin` role.

**Detection-tier classification (slice 353 / Q-13):**

- `detection_tier_actual`: manual_review (the #2068 design-audit walk)
- `detection_tier_target`: integration

The `/v1/me` integration suites minted bare-uuid subjects, which no real login
produces. The new test uses `user:<uuid>` and fails on the old lookup.

## D1 — roles key on `credstore.CallerUserID`

`resolveRoles` looks a user caller up by `credstore.CallerUserID` (#2183), the
one shared resolver; no new local strip. A principal that names no user (API
key, `bootstrap-admin`, `oauth_client:*`) keeps its raw `UserID`, which is what
the lookup used before, so its behavior is unchanged.

## D2 — /admin is super-admin only (Matt, 2026-11-14)

The ticket asked whether `/admin` is for tenant admins or super-admins. Matt's
call: **super-admin only**. `is_admin` on `/v1/me` stays `cred.IsAdmin`
(mapped from `atlas:super_admin`), and the server-side gates are unchanged.
The denial copy now says the section needs a platform super-admin and that a
tenant `admin` role is not enough.

## D3 — the web gates stop admitting tenant admins by role

Both web gates admit on `is_admin` OR a role in `/v1/me` `roles`. While
`roles` was always empty, they were super-admin gates in practice. D1 fills
`roles`, which would have widened them:

- `web/lib/admin-nav.ts` `ADMIN_NAV_ROLES` drops `admin` and `tenant_admin`,
  keeping `super_admin`. Otherwise a tenant admin sees an Admin entry that only
  leads to the denial page.
- `web/app/audit-log/layout.tsx` `AUDIT_LOG_ROLES` drops `admin`. The backend
  (`adminauditlog.callerAllowedUnified` + `HasUnifiedAuditLogRole`) admits
  super-admin plus `auditor` / `grc_engineer`, and the constant must match
  that SQL. A tenant admin admitted by the UI would have hit a 403.

Net effect: neither gate admits anyone it did not admit before this change.

## D4 — /v1/admin/controls/held-back is tenant-admin (Matt, 2026-11-17)

D2 keeps the `/admin` section super-admin only. `GET
/v1/admin/controls/held-back` (#2358) is an exception by Matt's decision of
2026-11-17: a tenant admin by live role (`authz.IsTenantAdmin`, read from
`user_roles` at request time, not a JWT claim) lists the stock controls the
boot seed holds back in their own tenant. The list is per tenant and RLS
bounds it, so it needs no platform-wide reach.

`admin.rego` lists the whole `controls` segment in
`tenant_admin_only_segments`, so any later route under
`/v1/admin/controls/` is tenant-admin-only by default, and no other role
reads it. Recorded under #2599.

## Out of scope (filed separately)

Two other role lookups still key on the raw subject. Both are authorization
behavior, which #2183 D2 deliberately left alone, so they are not changed here:

- `internal/authz/decision.go` `Engine.Decide` → `RolesFor(in.TenantID, in.User.ID)`.
- `internal/api/adminauditlog` `callerAllowedUnified(…, cred.UserID, …)`: a JWT
  auditor or grc_engineer is still refused by the audit-log API.

Both are tracked in #2239.
