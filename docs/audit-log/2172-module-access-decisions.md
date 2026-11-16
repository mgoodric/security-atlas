# 2172 — Module access for each member: decisions log

Issue: mgoodric/security-atlas#2172 (spec #2166, ADR 0025). After #2170
(tracer) and #2171 (every route belongs to a module).

## Detection-tier classification

- **detection_tier_actual:** none. No product bug surfaced. Unmounting the
  access gate makes `TestModuleAccess_ViewerNoAccessThenRoleDecisionThenSwitchedOff`
  and `TestModuleAccess_WildcardGainsNewlySwitchedOnModule` fail (the
  narrowed member reaches the role decision / a 200), and both pass once
  it is mounted again.
- **detection_tier_target:** integration (the gate through the real router,
  real Postgres, the live user_roles resolver).

## Decisions

### D1 — storage: one row per membership, NULL is "all", no row is "all"

`member_module_access (tenant_id, user_id, modules TEXT[] NULL)`. NULL is
the wildcard and an empty array is "core modules only". A member with no
row has "all", so new and existing members need no backfill and keep
today's behaviour. Setting "all" stores NULL rather than deleting, so the
table needs no DELETE policy. `user_id` is the `user_roles` key
(`identity.RolesKey`: the bare user uuid), read with the same key the role
resolver uses. FORCE RLS with SELECT / INSERT / UPDATE policies.

The access audit log (`member_module_access_audit_log`) is append-only by
RLS (SELECT + INSERT only) and by trigger (UPDATE / DELETE refused for any
role), written in the same transaction as the upsert, `before` -> `after`.
It is unified-log kind `module_access`, mapped to the `admin` resource in
`kindSourceResource`, and hidden from non-privileged Activity readers the
same way `feature_flag` rows are: both are tenant administration.

Revoking a membership does not delete the access row; re-adding the same
user restores their previous access. Nothing in #2166 asks for a reset, and
the row grants nothing on its own.

### D2 — one choke point, between the module gate and OPA

`moduleaccess.Gate` is one `root.Use` step after `featureflag.ModuleGate`
and before `authzmw`. It resolves the path's owner from the same registry
(`featureflag.Resolve`, decoded and raw path), and for an optional module
outside the caller's access answers `403` with
`{"error":"You don't have access to <Module>. Ask an administrator.","module":"<key>","module_name":"<Module>"}`.
Order follows ADR 0025 decision 3: switched off (404) is checked first and
wins for every caller; the OPA decision is unchanged and only runs once the
module is on and in access. So with access, a member gets exactly today's
role decision (a real viewer's `403 {"error":"forbidden"}` on questionnaires
included).

The common case costs one indexed read: a member with no stored narrowing
is never role-resolved. Only a narrowed member's roles are read (the
credential's derived roles plus live `user_roles`, the union OPA evaluates)
to decide the admin / auditor exemption. A store error fails closed (500),
unlike the module switch's fail-open: access narrows who may see what.

### D3 — who is exempt

Admin or auditor in either the derived or the live roles, or a confirmed
super-admin JWT. A member holding auditor plus viewer is exempt: #2166 says
auditors are unaffected, and their audit-period scoping is the limit on
them. A narrowed value stored on an exempt member is kept and ignored (it
bites only if they lose admin / auditor), which is what the "admin with a
narrowed access value" acceptance test exercises.

### D4 — an admin cannot narrow themselves

`PUT` with an explicit set on the caller's own user id is `400` (#2166
story 18). Setting yourself to "all" is allowed. Narrowing another admin is
allowed and ignored while they hold admin (D3).

### D5 — the admin API

`PUT /v1/admin/users/{id}/module-access`, gated on `authz.IsTenantAdmin`
(the live tenant admin role, #2428) and registered in the #2447 gate-site
and #2428 route tables. There is no `GET` of its own: OPA admits
grc_engineer on `admin` reads, so a tenant-admin-only read would disagree
with OPA, which the #2447 guard forbids; the value is read from
`module_access` on the user rows instead. The target must
be a `users` row in the caller's tenant (tenant predicate + RLS); another
tenant's member, or no user, is `404`. The body field `modules` is
required (`null` for all), so a typo cannot silently grant everything; a
core or unknown key is `400`. Both `/v1/admin/users` shapes carry
`module_access`: the within-tenant list from the store, the super-admin
cross-tenant list by subquery on the BYPASSRLS pool it already uses.

### D6 — `GET /v1/me/modules` reports `no_access`

A switched-on module outside access reads `no_access`, and its features read
`no_access` too. Switched off still reads `switched_off` for everyone.

### D7 — web

- `ModuleState` gains `no_access`; `gateNavItems` already shows only `on`,
  so the sidebar leaves those modules out with no further change.
- `FeatureGate` renders "You don't have access to <Module>. Ask an
  administrator." (`feature-no-access-state`) for `no_access`, naming the
  owning module even on a feature's page (vendor claims -> Third-party risk).
- Admin › Users gains a Module access column. Admin and auditor rows read
  "All modules (admin|auditor)" with no control. The optional-module list
  comes from `GET /v1/admin/features` (the server's registry), not a list of
  the web app's own. A super-admin's cross-tenant rows in another tenant show
  the value read-only, since the PUT acts inside the caller's tenant.
- Activity reads a `module_access` row as "<Person> changed the module access
  of member <id>". Module switch changes were already there as
  `feature_flag` rows ("enabled / disabled feature flag <key>").

### D8 — the Playwright tenant and the cross-tenant list

The e2e admin is a super-admin, whose Admin › Users list is the cross-tenant
one, paged by tenant id. The spec reads the real BFF with `limit=200` and
narrows it to its own tenant (a `page.route` that fetches the real response),
so other specs' users cannot push the member off the first page. The save
goes through the real BFF and API unmocked.
