# 2401 — A JWT session with an empty role claim derives nothing: decisions log

Issue: mgoodric/security-atlas#2401, found in the review of #2390 (#2381).
`jwtmw` copies the JWT's role claim for the current tenant into the
synthesized credential's `OwnerRoles`, and `authz.derivedRolesFor` sent an
empty `OwnerRoles` to its `default` branch, which derives `grc_engineer`. A
user with no `user_roles` row in the tenant gets exactly that empty claim at
login, so revoking a user's only role lasted until their next login.

## Detection-tier classification

- **detection_tier_actual:** manual_review. Found reviewing #2390, whose
  unit table pinned `"JWT empty claim" -> grc_engineer`.
- **detection_tier_target:** integration.
  `TestDecide2401_EmptyClaimRoleMatrix` (real Postgres RLS) and
  `TestIntegrationDeviceFlow2401_RolelessApproverDerivesNothing` (approve,
  redeem, real `jwtmw`) fail on the old derivation and pass with the fix.

## Step 1 — does any login path already refuse a role-less user?

No. Every path mints a session and a token:

| Path                                                    | Where the claim comes from                                    | Evidence                                                                                           |
| ------------------------------------------------------- | ------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| Local login (`signInLocal` -> `POST /auth/local/login`) | `DBUserResolver.ResolveForOAuth`                              | `TestLocalLogin_SingleTenant_DefaultsTenantID`: a user created with no role logs in, 200 + session |
| OIDC callback -> `/oauth/authorize` -> PKCE redemption  | `ResolveForOAuth`, snapshotted on the auth code               | `TestIntegrationDBUserResolver_NoRolesYieldsEmptyMap`: no rows -> no error, empty role list        |
| Device grant                                            | the approving JWT's `OwnerRoles`, copied onto the device code | `TestIntegrationDeviceFlow2401_RolelessApproverDerivesNothing`                                     |
| Token exchange / tenant switch                          | the subject token's `Roles` map, copied                       | (copy; no lookup)                                                                                  |

`ResolveForOAuth` adds a tenant to `atlas:roles` only when it has rows, so
the claim for that tenant is absent and `jwtmw` sets `OwnerRoles` to nil.

## Decisions

### D1 — an allow-list: only `oauth_client:` keeps the empty-claim default

A `jwt:` credential with an empty claim derives nothing unless its subject
is `oauth_client:<id>`. That covers a `user:<uuid>` session, a bare uuid,
a non-canonical uuid, the nil uuid, a test-harness subject and any shape not
known today.

The first draft was a deny-list: derive nothing when the subject names a
user (`identity.UserID`). The dispatcher asked for the allow-list instead,
because a deny-list fails open for a subject shape nobody has named yet.

Kept, with the reason:

| Principal                                        | Derives        | Why it keeps the default                                                                                                                        |
| ------------------------------------------------ | -------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| `oauth_client:<id>` JWT (client_credentials)     | `grc_engineer` | Names no user, so no `user_roles` row can stand in. Its claim is always empty (`token.go` mints `Roles: {}`). Connectors push evidence with it. |
| API keys (`key_...`), bound or unbound, no label | `grc_engineer` | Not a JWT; out of scope. #2402 handles labelled keys.                                                                                           |
| Bootstrap / in-memory credstore credentials      | `grc_engineer` | Not a JWT; out of scope.                                                                                                                        |
| super_admin JWT                                  | `admin`        | `IsAdmin` is decided before the claim is read; unchanged.                                                                                       |

### D2 — no refusal at login in this PR

A role-less person still signs in and gets a session that can do nothing in
the tenant: every tenant route answers 403. The dispatcher ruled login
refusal out of scope. Refusing at login is the option to put to Matt: it
would give a clear "no access" message before the app shell renders, at the
cost of a new failure mode on every login path (local, OIDC, device) and a
decision on what a multi-tenant member with no role in one tenant sees.

### D3 — what the web app shows a role-less user

`/` redirects to `/dashboard`. Each dashboard panel owns its own query and
renders its own error state on a 403. Only a 401 redirects to `/login`, so
there is no redirect loop and no 500. The app has no dedicated "no access"
page, so there is nothing for a Playwright check to pin. A page that says
"you have no role in this tenant, ask an admin" would be clearer than six
panel errors; that is a follow-up, not this fix.

### D4 — a role granted after login applies at once

A user whose claim is empty but who has since been granted a role decides
on that live row alone (#2239's resolver merge). The matrix asserts each
canonical role decides exactly as the grant does, nothing more.

### D5 — test-harness tokens get explicit roles, not a fallback

`testjwt.ViewerFor` mints a `test-viewer:<tenant>` subject with no claim. It
derived `grc_engineer` through the same default, and the allow-list removes
that for it too. The dispatcher asked that harness fixtures carry their roles
explicitly rather than keep a "names no user" fallback in production code.

Two fixtures depended on the default: `newModuleGateFixture` and
`newModulesEnv` (`internal/api`). Each already granted its non-admin subject
`viewer` through a test roles resolver, but that subject was really running
as `viewer` + `grc_engineer`, and `viewer` alone cannot read questionnaires.
Both now grant `grc_engineer` explicitly, and the principal is renamed from
`viewer` to `member` so the name no longer claims a role it does not hold.
The other `ViewerFor` integration packages run in this PR (listed in the PR
body) pass unchanged.

`ViewerFor` itself is unchanged: it has no database and no way to grant a
role, and its token now really is the "authenticates, unlocks nothing"
credential its doc comment describes.

### D6 — the dry run prints no identifiers

`scripts/dry-run-2401-roleless-users.sql` is read-only and counts-only. It
numbers tenants 1..N in `tenant_id` order instead of printing their ids, so
its output names no tenant, user or email. Super_admins are counted
separately because they keep `admin`. It has been run only against a scratch
cluster, never a live install.
