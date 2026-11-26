# 2496 — A user's JWT session derives nothing from its role claim: decisions log

Issue: mgoodric/security-atlas#2496, found in the review of #2495 (#2494).
`jwtmw` copies the JWT's role claim for the current tenant into the
synthesized credential's `OwnerRoles`. After #2381 (read-only claims) and
#2401 (empty claims), `authz.derivedRolesFor` still sent any other non-empty
claim to `len(cred.OwnerRoles) > 0 -> control_owner`. A session whose claim
said `admin` or `grc_engineer` therefore kept control_owner reads and writes
after every live `user_roles` row was revoked, until the token expired.
`HasTenantRole` (#2495) already reported false, so the web showed the
no-role page while the API still admitted the session.

## Detection-tier classification

- **detection_tier_actual:** manual_review. Found reviewing #2495.
- **detection_tier_target:** integration.
  `TestDecide2496_StaleClaimRoleMatrix` (real Postgres RLS, one credential
  held across grant, revoke and re-grant) fails on the old derivation and
  passes with the fix.

## Decisions

### D1 — a user's `jwt:` session derives nothing, whatever the claim says

`derivedRolesFor` now has one case for every `jwt:` credential whose subject
is not `oauth_client:<id>`: it returns nil. That replaces the two narrower
cases #2381 and #2401 added (read-only claim, empty claim) and covers the
admin, grc_engineer, control_owner and unknown-label claims they left on the
`control_owner` fallthrough. It is the same allow-list shape as #2401 D1:
only a client_credentials machine token reaches the cases below, and any
other subject shape, known or not, derives nothing.

The #2381 read-only case is kept for machine tokens only, so an
`oauth_client:` token still never derives control_owner from a viewer or
auditor label. Its claim is always empty in practice (`token.go` mints
`Roles: {}`), so this is belt-and-braces.

| Principal                              | Derives from the credential                       | Change    |
| -------------------------------------- | ------------------------------------------------- | --------- |
| `jwt:` user session, any claim         | nothing; live `user_roles` only                   | narrowed  |
| `jwt:` super-admin (`IsAdmin`)         | `admin` (from `IsAdmin`, not the claim)           | unchanged |
| `jwt:` `oauth_client:` machine token   | claim-based, as before (`grc_engineer` for empty) | unchanged |
| API key (`key_…`), bound or unbound    | as #2402 left it                                  | unchanged |
| bootstrap / in-memory admin credential | as before                                         | unchanged |

### D2 — where live roles enter the decision

HTTP: `authzmw.Middleware` calls `authz.BuildInput` (derived roles, now
empty for a user session) and then `Engine.Decide`. `Decide` calls
`RolesResolver.RolesFor(tenant, user)` against `user_roles` and merges those
rows into `in.User.Roles` **before** the OPA query is evaluated
(`internal/authz/decision.go`, the resolver hook at the top of `Decide`).
So the OPA decision for a user session is made on the live rows alone; no
lookup moves. After an allow, `withTenantRoles` stamps the same live rows on
`cred.TenantRoles` for the handler gates (#2428). The other `Decide` callers
(`calendar`, `search`, `adminauditlog` payload gate) go through the same
`BuildInput` + `Decide` pair. `moduleaccess.exempt` unions `BuildInput`
roles with a direct `RolesFor` call, so it also sees live rows only.

gRPC: user JWTs do not reach it. `authInterceptor` (`internal/api/server.go`)
resolves the bearer through `credstore.Store.Authenticate`, which knows API
keys and in-memory credentials only; `jwt:` credentials are synthesized by
the HTTP `jwtmw` alone. Nothing to change there.

### D3 — super-admin stays as is

`jwtmw` maps the re-checked `atlas:super_admin` claim to `IsAdmin` and
`IsApprover`; `derivedRolesFor` reads those before the new case, so a super
admin still derives `admin`. It is a platform bit, not a tenant role, and is
out of scope here.

### D4 — handler gates that still read `OwnerRoles`

Several handler-level checks still read `cred.OwnerRoles` directly (for
example `policyacks`' ack caller, `adminvendors.callerAllowedExport`,
`admindemo.requireAdmin`, and the `IsAdmin || IsApprover ||
len(OwnerRoles) > 0` read gates), and `oauth/device_approve.go` copies it
into a device token. A gate behind OPA runs only after OPA admits, so a
session with no live role never reaches it. A session whose live role is
narrower than its stale claim (claim `grc_engineer`, live `viewer`) reaches
it on routes OPA admits for viewer, and a claim-reading gate can then admit
more than the live role would. For example, a viewer may read
`GET /v1/me/acknowledgments`, where the stale claim still matches a policy
whose `acknowledgment_required_roles` names `grc_engineer`. Device approval sits outside
authzmw entirely: a revoked `admin` claim is copied into the approved
token's roles and re-minted with a fresh expiry. Changing those sites is
outside this ticket's acceptance criteria; it is reported as a follow-up
(#2505) rather than widened here.

_Corrected by #2505._ This entry first gave the vendor export as the
example. That case cannot happen: `/v1/admin/vendors` is resource `admin`,
which OPA does not admit for viewer, so a narrower live role never reaches
`callerAllowedExport`.

## Fixtures updated

Tests that pinned the old claim-derived control_owner for a user session:

- `internal/authz/jwt_readonly_roles_2381_test.go`: JWT control_owner,
  grc_engineer, admin and auditor+control_owner claims now derive nothing.
- `internal/authz/jwt_empty_claim_2401_test.go`: "JWT user, control_owner
  claim" now derives nothing.
- `internal/authz/jwt_readonly_roles_2381_integration_test.go`: every user
  JWT may narrow; a principal whose live roles do not cover control_owner
  must lose something and is denied every write.
- `internal/authz/tenant_roles_2428_test.go`: the stamping cases that had
  no live role now hold live viewer (with no live role OPA denies and the
  handler is never reached).
- `internal/authz/tenant_roles_2428_integration_test.go`: a revoked admin's
  walkthrough write is now refused by OPA, not the handler gate.
