# #2503 — tenant-scoped /v1/admin pages for organisation admins — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2503
**Date:** 2026-11-17

Matt's decision (2026-11-17): organisation admins manage their own
organisation's admin pages. Until now every `/v1/admin/*` route and `/v1/me`
`is_admin` were super-admin only (#2216 D2, #2428 D4), held by the handlers'
`cred.IsAdmin` gates. OPA's `admin.rego` admitted any `admin` role on every
`/v1/admin/*` segment except `crosswalk-edge-remap-proposals`, so the handlers
were the only thing keeping a tenant admin out.

This is the first of the PRs the ticket splits into: the inventory, the
mechanism, and the users routes. The other tenant-scoped routes follow in
later PRs (D6).

**Detection-tier classification (slice 353 / Q-13):**

- `detection_tier_actual`: none (a product decision, not a bug found during
  the work).
- `detection_tier_target`: integration.
  `TestTenantAdmin2503_*` (real ES256 JWTs through jwtmw, tenancymw, authzmw
  and the real adminusers handlers on Postgres) fails on the old rego and
  handler gate and passes on the new ones.

## Words used here

- **Admin credential**: `cred.IsAdmin`. jwtmw sets it from the verified
  `atlas:super_admin` claim; an admin-flagged API key also carries it.
- **Tenant admin by role**: a caller whose live `user_roles` in its current
  tenant include `admin`, with no admin credential. `authz.IsTenantAdmin`
  admits both.

## D1 — the inventory

Every `/v1/admin/*` route and method. "OPA before" and "OPA after" are the
decision for a tenant admin by role. A super-admin and an admin-flagged key
keep exactly what they had.

| Route                                                                                                              | Class                                           | Reason                                                                                                              | Handler gate before                                              | Handler gate after                             | OPA before | OPA after                                       |
| ------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------- | ---------------------------------------------- | ---------- | ----------------------------------------------- |
| `GET /v1/admin/users`                                                                                              | tenant                                          | own tenant's members, under RLS; super-admins get the cross-tenant list                                             | `cred.IsAdmin`                                                   | `IsTenantAdmin`                                | allow      | allow                                           |
| `GET /v1/admin/users/{id}`                                                                                         | tenant                                          | own member, under RLS (foreign id 404)                                                                              | `cred.IsAdmin`                                                   | `IsTenantAdmin`                                | allow      | allow                                           |
| `PATCH /v1/admin/users/{id}/roles`                                                                                 | tenant                                          | own member's roles; canonical roles only, last-admin guard                                                          | `cred.IsAdmin`                                                   | `IsTenantAdmin`, foreign id 404                | allow      | allow                                           |
| `PUT /v1/admin/users/{id}/module-access`                                                                           | tenant                                          | own member's module access (#2172)                                                                                  | `IsTenantAdmin`                                                  | unchanged                                      | allow      | allow                                           |
| `POST /v1/admin/users/assign`                                                                                      | tenant within own tenant; platform cross-tenant | cross-tenant needs super_admin in the handler                                                                       | super_admin or `cred.IsAdmin`                                    | super_admin or `IsTenantAdmin`                 | allow      | allow                                           |
| `POST /v1/admin/users/revoke`                                                                                      | tenant within own tenant; platform cross-tenant | as assign                                                                                                           | super_admin or `cred.IsAdmin`                                    | super_admin or `IsTenantAdmin`, foreign id 404 | allow      | allow                                           |
| `GET /v1/admin/controls/held-back`                                                                                 | tenant                                          | own tenant's held-back controls (#2358)                                                                             | `IsTenantAdmin`                                                  | unchanged                                      | allow      | allow                                           |
| `GET/PUT/DELETE /v1/admin/llm-routing`                                                                             | tenant                                          | own tenant's LLM routing (#2428)                                                                                    | `IsTenantAdmin`                                                  | unchanged                                      | allow      | allow                                           |
| `GET /v1/admin/evidence/export`                                                                                    | tenant                                          | own tenant's ledger export (#2507)                                                                                  | `IsTenantApprover`                                               | unchanged                                      | allow      | allow                                           |
| `GET /v1/admin/policies/export`                                                                                    | tenant                                          | as above                                                                                                            | `IsTenantApprover`                                               | unchanged                                      | allow      | allow                                           |
| `GET /v1/admin/exceptions/export`                                                                                  | tenant                                          | as above                                                                                                            | `IsTenantApprover`                                               | unchanged                                      | allow      | allow                                           |
| `GET /v1/admin/samples/export`                                                                                     | tenant                                          | as above                                                                                                            | `IsTenantApprover`                                               | unchanged                                      | allow      | allow                                           |
| `POST /v1/admin/policies/import`                                                                                   | tenant, later PR                                | bulk-writes published policies with history; opening it is a separate call                                          | `cred.IsAdmin`                                                   | unchanged                                      | allow      | allow (segment `policies`; the handler refuses) |
| `GET/PATCH /v1/admin/sso`, `POST /v1/admin/sso/preflight`                                                          | tenant, later PR                                | own tenant's OIDC config                                                                                            | `cred.IsAdmin`                                                   | unchanged                                      | allow      | deny                                            |
| `GET/POST /v1/admin/scim-credentials`, `DELETE …/{id}`                                                             | tenant, later PR                                | own tenant's SCIM bearer                                                                                            | `cred.IsAdmin`                                                   | unchanged                                      | allow      | deny                                            |
| `GET/POST /v1/admin/credentials`, `POST …/{id}/rotate`, `POST …/{id}/revoke`                                       | tenant, later PR                                | own tenant's API keys; mint carries an `is_admin` flag that must be refused, and roles must not exceed the minter's | `cred.IsAdmin`                                                   | unchanged                                      | allow      | deny                                            |
| `GET/POST /v1/admin/group-role-mappings`, `DELETE …/{id}`                                                          | tenant, later PR                                | own tenant's IdP group-to-role map                                                                                  | `cred.IsAdmin`                                                   | unchanged                                      | allow      | deny                                            |
| `GET /v1/admin/features`, `PATCH …/{key}`                                                                          | tenant, later PR                                | own tenant's module switches                                                                                        | `cred.IsAdmin`                                                   | unchanged                                      | allow      | deny                                            |
| `GET /v1/admin/audit-log`                                                                                          | unsure, platform for now                        | feature-flag flip log; see D5                                                                                       | `cred.IsAdmin`                                                   | unchanged                                      | allow      | deny                                            |
| `GET /v1/admin/audit-log/unified`, `…/export`                                                                      | unsure, platform for now                        | see D5                                                                                                              | `cred.IsAdmin` or auditor/grc_engineer row                       | unchanged                                      | allow      | deny                                            |
| `GET /v1/admin/audit-periods/export`                                                                               | unsure, platform for now                        | handler admits auditor/grc_engineer, not a tenant admin                                                             | `cred.IsAdmin` or auditor/grc_engineer                           | unchanged                                      | allow      | deny                                            |
| `GET /v1/admin/vendors/export`                                                                                     | unsure, platform for now                        | as above                                                                                                            | `cred.IsAdmin` or auditor/grc_engineer                           | unchanged                                      | allow      | deny                                            |
| `GET /v1/admin/demo/status`, `POST …/seed`, `POST …/teardown`                                                      | platform                                        | seed and teardown create and delete a whole tenant (#1851)                                                          | status: `cred.IsAdmin` or live admin; seed/teardown: super_admin | unchanged                                      | allow      | deny (D4)                                       |
| `POST /v1/admin/install/reset-bootstrap`                                                                           | platform                                        | install-wide bootstrap token                                                                                        | `cred.IsAdmin`                                                   | unchanged                                      | allow      | deny                                            |
| `GET/POST /v1/admin/tenants`                                                                                       | platform                                        | lists and creates tenants                                                                                           | super_admin                                                      | unchanged                                      | allow      | deny                                            |
| `GET/POST /v1/admin/super-admins`, `DELETE …/{user_id}`                                                            | platform                                        | super-admin management                                                                                              | super_admin                                                      | unchanged                                      | allow      | deny                                            |
| `POST /v1/admin/authz-bundle/reload`                                                                               | platform                                        | swaps the install's policy bundle                                                                                   | super_admin                                                      | unchanged                                      | allow      | deny                                            |
| `POST /v1/admin/crosswalk-edges/{id}/tier`                                                                         | platform                                        | global catalog rows (#1757)                                                                                         | super_admin                                                      | unchanged                                      | allow      | deny                                            |
| `GET/POST /v1/admin/crosswalk-edge-remap-proposals…`                                                               | platform                                        | global catalog rows (#1777)                                                                                         | super_admin                                                      | unchanged                                      | deny       | deny                                            |
| `POST /v1/admin/framework-versions/{id}/promote`, `…/revert`, `…/migrations:suggest`, `…/migrations/{id}/decision` | platform                                        | global catalog rows (#1757)                                                                                         | super_admin                                                      | unchanged                                      | allow      | deny                                            |
| `GET /v1/admin/framework-versions/migrations`                                                                      | platform                                        | global catalog queue                                                                                                | `cred.IsAdmin`                                                   | unchanged                                      | allow      | deny                                            |

grc_engineer, auditor, control_owner and viewer get the same OPA answer on
every row as before: their rules are separate from `admin.rego`, which is the
only file this changes.

## D2 — the OPA mechanism: a named segment set, no wildcard

`admin.rego`'s single `has_role("admin")` rule is split into three:

1. `admin` role on any resource that is not `admin` (unchanged).
2. `admin` role on `admin/<segment>` only for `tenant_admin_segments`:
   `users`, `controls`, `llm-routing`, `evidence`, `policies`, `exceptions`,
   `samples`.
3. `admin` role plus an admin credential on any other `admin/<segment>`,
   except the `super_admin_only_segments` (unchanged).

BuildInput adds `input.user.attrs.is_admin_credential` (`cred.IsAdmin`) so
rule 3 can tell the two callers apart; an admin credential derives the
`admin` role in `derivedRolesFor` as before. The rego is in
`internal/authz/rego_bundle/admin.rego`, mirrored byte for byte in
`policies/authz/admin.rego` (#2410).

The segment set starts with exactly the segments a tenant admin by role
already reached through a handler (D1 rows marked unchanged and allow), plus
`users`. Nothing a tenant admin could do before is refused now, except
`demo/status` (D4). Each later PR adds its segment to the set with its
handler change.

`adminauditlog`'s payload gate asked OPA `write` on `admin` with an empty id
to mean "is an admin". It now asks on `admin/users`, which the module-access
rows it gates live under, so its answer is unchanged.

## D3 — the users routes and their guards

- `requireAdmin` in `adminusers` is `authz.IsTenantAdmin`. Every query runs
  under the caller's tenant RLS; the tenant comes from the credential, never
  from a path or body value. Cross-tenant assign and revoke keep their
  super_admin check before this one.
- `PATCH …/{id}/roles` checks the target is a member of the caller's tenant
  before writing. Before, a foreign id deleted nothing, inserted an orphan
  `user_roles` row in the caller's tenant, and answered 404 only on the
  refetch. It is now a 404 with nothing written.
- `POST …/revoke` within the caller's tenant checks the same and answers 404
  for a foreign id, where it answered 204 having changed nothing.
- `super_admin` is not a role: `PATCH …/roles` and `assign` accept only the
  five canonical roles, so a tenant admin cannot grant it. Granting the
  `admin` role to a member of its own tenant is allowed, as for any admin.
- The last-admin guards (#2261/#2263/#2273) are unchanged and run for a tenant
  admin as for anyone; the integration test checks self-demotion and revoke of
  the only admin are 409.

## D4 — demo status is platform

A tenant admin by role could read `GET /v1/admin/demo/status` (whether the
demo seed is switched on). The demo page seeds and tears down whole tenants,
so the segment is platform and OPA now refuses the status read too. The web
probes it only for a super-admin. The answer is an env-var flag, so nothing a
tenant admin needs is lost.

## D5 — audit-log stays where it is (for Matt; resolved in D9)

grc_engineer's and auditor's reads of `/v1/admin/audit-log/unified` and its
export are their own rules (slice 124 D5) and are unchanged. Whether a tenant
admin by role should read the unified log, and whether `audit-log` should
become tenant-admin-only (`tenant_admin_only_segments`) instead of staying
readable by grc_engineer and auditor, is not settled by "users, roles, module
access, settings, keys, SCIM/OIDC". It stays platform for a tenant admin and
unchanged for the others until Matt decides. The same goes for the
audit-periods and vendors exports.

## D5a — grc_engineer still reads `admin/users` in OPA (for Matt; resolved in D10)

`grc_engineer.rego`'s blanket read admits `GET /v1/admin/users` and
`GET /v1/admin/users/{id}`. The handler refused grc_engineer there before
(`cred.IsAdmin`) and still does (`IsTenantAdmin`), so access is unchanged.
It is pinned in `handlerNarrower2428`. Adding `users` to
`tenant_admin_only_segments` would make the layers agree, but it changes a
grc_engineer OPA answer, which this ticket leaves alone.

## D6 — what the later PRs carry

Each adds its segment to `tenant_admin_segments` and switches its handler to
`IsTenantAdmin`, with an integration test:

- `sso` (incl. preflight), `scim-credentials`, `group-role-mappings`,
  `features`.
- `credentials`: a tenant admin's mint must refuse `is_admin: true` and
  `is_approver`, and `owner_roles` no wider than the minter's own live roles;
  the current handler takes `is_admin` from the request body.
- `policies/import`, if Matt wants it opened.

## D7 — web

- `/v1/me` already carried `is_tenant_admin` (#2428 D4); `is_admin` keeps
  meaning super-admin.
- `AdminMe` (`lib/api/self.server.ts`) carries `is_tenant_admin`, true only on
  a literal `true`.
- The sidebar Admin entry (`shouldShowAdminEntry`) shows on `is_admin` or
  `is_tenant_admin`. The role-string list stays `super_admin` only: a role
  string is the session's claim, not a live grant.
- `/admin` admits both. A tenant admin's section nav lists Admin and Users.
  The overview shows the Users tile only, and does not fetch the API key or
  feature counts.
- The platform pages (SSO, API keys, Features, Audit, Super admins, Tenants,
  Demo) wrap their children in `PlatformAdminGate`, which renders a "this page
  is for platform super-admins" panel unless `is_admin`. The handlers and OPA
  refuse those routes anyway; the gate keeps the page from rendering a screen
  of 403s.
- Other `is_admin` reads in the web were left alone: `/audit-log` and
  `/activity` (`canReachAuditLog`) follow the backend's audit-log gate (D5),
  `FeatureGate` uses it for module visibility, not the admin section, and
  the Settings page's API tokens card follows `/v1/admin/credentials`, which
  moves with the credentials PR (D6).

## Part 2 — SSO, and Matt's calls on D4, D5, D5a and D6

The second PR of the split. Matt decided the four open items on
2026-11-18: D5 and D5a as below, D4 as built (demo status stays platform),
and D6 as written (the credentials mint guard lands in the credentials PR,
before `credentials` joins the segments).

### D8 — SSO is tenant-scoped

`GET/PATCH /v1/admin/sso` and `POST /v1/admin/sso/preflight` admit
`authz.IsTenantAdmin`, and `sso` joins `tenant_admin_segments`. The handler
keys every read and write on the credential's tenant under RLS (one
`oidc_idp_configs` row per tenant), and the sign-in resolver reads the same
row (#2602), so a tenant admin's change affects only its own sign-in. The
issuer validation and SSRF guard are unchanged and apply to a tenant admin
as to anyone. The `trust_unverified_email_for_link` switch (#1944) is in the
same config: a tenant admin can turn it on for its own tenant. It links an
IdP identity to an existing member of that tenant by email, the same
authority the admin already has through Users.

grc_engineer's blanket read still admits `GET /v1/admin/sso` in OPA; the
handler refuses it, as before. It is pinned in `handlerNarrower2428`.

### D9 — org admins read and export their own audit log (Matt, 2026-11-18)

`audit-log` joins `tenant_admin_segments`. The handler gates move from
`cred.IsAdmin` to `authz.IsTenantAdmin`:

- `GET /v1/admin/audit-log` (the feature-flag flip log): `requireAdmin`.
- `GET /v1/admin/audit-log/unified` and `…/export`: `callerAllowedUnified`
  takes `IsTenantAdmin` in place of the flag; the live auditor/grc_engineer
  `user_roles` lookup is unchanged, so grc_engineer keeps its read.
- `GET /v1/activity/unified` passes the same value as its privileged view,
  so an org admin sees the rows a privileged caller sees there (feature-flag
  rows and other members' own-settings rows), as a super-admin does.

All four run under the credential's tenant RLS. On the web, the export bar
on Activity (`canReachAuditLog`) admits `is_tenant_admin`.

### D10 — Users is org-admin only (Matt, 2026-11-18)

`users` joins `tenant_admin_only_segments`, so grc_engineer's blanket read no
longer admits `GET /v1/admin/users` in OPA; the handler and OPA now agree,
and the `handlerNarrower2428` pin for it is gone. One consequence: the
unified log's module-access rows (#2172) take their payload visibility from
`admin/users`, so a grc_engineer now sees those rows without their payload.
The payload-gate test pins that.

The payload gate used to cache one OPA answer for every kind whose source
is `admin`, and part 1 asked it on `admin/users`. Once `users` became
tenant-admin only, that answer also hid the decision-log and feature-flag
payloads from grc*engineer, which `TestPayloadGate*\*` caught. The gate now
asks per segment (`kindSourceAdminSegment`): decision rows on `audit-log`,
feature-flag rows on `features`, module-access rows on `users`. So
grc_engineer keeps the first two and loses only the third.

### D11 — inventory rows after part 2

| Route                                         | Class                     | Handler gate                                 | OPA, tenant admin by role | OPA, grc_engineer                        |
| --------------------------------------------- | ------------------------- | -------------------------------------------- | ------------------------- | ---------------------------------------- |
| `GET/PATCH /v1/admin/sso`, `POST …/preflight` | tenant                    | `IsTenantAdmin`                              | allow                     | read allow (handler refuses); write deny |
| `GET /v1/admin/audit-log`                     | tenant                    | `IsTenantAdmin`                              | allow                     | allow (handler refuses)                  |
| `GET /v1/admin/audit-log/unified`, `…/export` | tenant                    | `IsTenantAdmin` or live auditor/grc_engineer | allow                     | allow                                    |
| `GET /v1/admin/users…`                        | tenant, tenant-admin only | `IsTenantAdmin`                              | allow                     | deny (was allow)                         |

The other D1 rows are unchanged.

### D12 — tests

- `TestTenantAdmin2503_SSOAndAuditLog`: a tenant admin by role saves and
  reads its SSO config, another tenant's admin sees none of it, and the
  feature-flag log, unified read and export all answer 200.
- `TestTenantAdmin2503_OtherRolesRefused`: grc_engineer's `GET
/v1/admin/users` is refused by OPA before the handler runs.
- Both fail on main's code (the SSO read is a 403, the grc_engineer read
  reaches the handler) and pass on this PR's.
- `slice124` / `slice135`: a tenant admin by role is now admitted to the
  unified log and its export.
- `gate_sites_2447` / `routes2428`: the SSO and feature-flag-log gates are
  rows. The unified read, export and activity gates are listed with no route
  row: the unified pair adds a live DB role lookup a cred-only row cannot
  express, and the activity call only picks the view.
