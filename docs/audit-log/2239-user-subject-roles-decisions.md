# 2239 — Assigned roles apply to JWT users: decisions log

Issue: mgoodric/security-atlas#2239. `Engine.Decide` looked `user_roles` up by
the raw credential `UserID`. A local-login, OIDC, PKCE or device-grant JWT
carries the subject `user:<uuid>`, and role assignment (#2183 / #2186) stores
the bare uuid, so the lookup missed every row and OPA decided on
credential-derived roles alone. The unified audit-log role probe
(`adminauditlog.callerAllowedUnified`) had the same raw-subject lookup.

Matt's call (2026-11-15, issue comment): fix it, with a dry run before rollout.

## Detection-tier classification

- **detection_tier_actual:** manual_review. Found by reading `decision.go`
  while fixing #2216, then reproduced on real Postgres by the #2118 build
  (a `user:<uuid>` auditor got 403 on `GET /v1/me/audit-periods`).
- **detection_tier_target:** integration. `TestDecide2239_RoleMatrixPerPrincipalKind`
  now fails on the old lookup (the password user's admin grant and the OIDC
  user's auditor grant change no decision) and passes with the fix.

## Decisions

### D1 — one key rule, `identity.RolesKey`, that rewrites only the prefixed form

`identity.RolesKey(principal)` returns the bare uuid for `user:<uuid>` and
returns every other principal unchanged. Both lookups use it:
`DBRolesResolver.RolesFor` and `callerAllowedUnified` (normalised inside the
probe, so its three callers in `activity.go`, `export.go` and `unified.go`
are covered at once).

`identity.UserID` alone was not used as the key because it also accepts a bare
uuid, and from a string alone it cannot tell a user from an unbound key whose
id happens to parse as a uuid (its own doc says so). Rewriting only the
`user:` form keeps every non-JWT principal on exactly the key it used before:
bare-uuid bound keys, `key_...`, `bootstrap-admin` and `oauth_client:<id>`
are looked up as themselves. That makes "machines and keys are unchanged" a
property of the code, not of the data.

`authz.Input.User.ID` stays the raw principal. Only the lookup key changes,
the same split the auditor attrs resolver already uses.

### D2 — the merge stays a union

`Decide` merges resolver roles into the derived roles (`mergeRoles`); it does
not replace them. This PR leaves that alone, so the fix can only add roles to
a JWT user. It cannot narrow anyone. The one way to lose a match is a
`user_roles.user_id` stored with the `user:` prefix, which would stop
matching; role assignment writes the bare uuid, and the dry run counts any
such rows (query 3), expected 0.

### D3 — an inactive user's role rows are ignored

Found by the leftover-assignment test the #2372 review asked for
(`TestPeriodAuditors2239_RealLoginAndLeftoverAssignment`). The admin API's
revoke deletes `user_roles` (and with `remove_membership` also disables the
user and revokes sessions and keys), but a SCIM deactivation only sets
`users.status = 'disabled'` and keeps the rows. An access JWT minted before
the deactivation stays valid until it expires. Before this fix that token
missed `user_roles` anyway. With D1 it would have picked up the auditor role
again, and through it every leftover `auditor_assignments` row: the test saw
200 on `/v1/me/audit-periods` and 201 on a note.

So `RolesFor` returns no rows when the key names a `users` row whose status
isn't `active` (`NOT EXISTS` on `users.id`, which uses the primary key). It
fails closed. A principal that names no user has no `users` row and isn't
affected. An API key bound to a disabled user drops to its derived role, which
is narrower than before, so the dry run counts those users (query 4). The
admin disable path already revokes such keys (#1915).

### D4 — `/v1/activity/unified` matches the caller by uuid

`ActivityList` compared `me_audit_log.actor_id` (a bare uuid) to the raw
`cred.UserID`. A JWT caller never saw their own me-rows, and the meta-audit
row the query writes was recorded under the nil uuid. The caller now resolves
through `credstore.CallerUserID`, which strips `user:` and still treats an
unbound key as a non-user. Covered by `TestActivity2239_JWTCallerSeesOwnMeRows`.

### D5 — #2381 is not folded in

#2381 is about JWT viewers and auditors being able to write evidence,
artifacts and walkthroughs. That comes from `jwtmw` putting the role claim in
`OwnerRoles`, which `BuildInput` derives as `control_owner`. This PR keeps the
union with the derived role (D2), so that write access remains. Closing it
means narrowing derivation, which removes access and is a separate decision.
Left to #2381.

## Blast radius

Who changes: **active users holding at least one `user_roles` row, on a JWT
session** (password login, OIDC, PKCE, device grant).

| Principal                                  | Effective roles before                            | After                                          |
| ------------------------------------------ | ------------------------------------------------- | ---------------------------------------------- |
| JWT user with `user_roles` rows            | `control_owner` (derived from the JWT role claim) | `control_owner` ∪ assigned roles               |
| JWT super_admin with `user_roles` rows     | `admin`                                           | `admin` ∪ assigned roles (no practical change) |
| JWT user with no `user_roles` row          | `grc_engineer` (derived)                          | unchanged                                      |
| User-bound API key (bare uuid)             | derived ∪ assigned                                | unchanged (already matched)                    |
| Unbound key (`key_...`), `bootstrap-admin` | derived ∪ rows under that id                      | unchanged                                      |
| OAuth client (`oauth_client:<id>`)         | derived ∪ rows under that id                      | unchanged                                      |

Concretely, per assigned role a JWT user now gains:

- `admin`: every tenant-scoped write, approve and publish, user admin, the
  unified audit log. The largest change: an assigned admin signing in through
  the browser was until now held to `control_owner`.
- `grc_engineer`: risk/policy/control writes, approvals, samples, dashboard
  export, the unified audit log.
- `auditor`: auditor reads (samples, dashboard export), the unified audit log,
  and audit-period ABAC hydration (the attrs resolver only runs once
  `auditor` is in the role set), which #2118 depends on.
- `viewer`: nothing beyond `control_owner`.

Also:

- The unified audit-log API (`/v1/admin/audit-log/unified`,
  `/v1/admin/audit-log/export`, privileged `/v1/activity/unified`) admits JWT
  users with an `auditor` or `grc_engineer` row.
- Revoking an assignment now takes effect for the assigned role on the next
  request, since the resolver reads live rows. The `control_owner` derived
  from the JWT claim still lasts until the token expires, as before.
- A disabled user's role rows no longer apply anywhere (D3). The only
  narrowing in this PR is an API key bound to a disabled user (dry-run
  query 4).
- `/v1/activity/unified` shows a JWT caller their own me-rows and records
  their query under their uuid (D4).
- The #2118 Playwright auditor now signs in with a `user:<uuid>` token,
  replacing the bare-uuid workaround (#2118 D5).

The dry run (`scripts/dry-run-2239-roles.sql`) prints these changes as counts
per tenant and (before, after) role set, with no user ids or emails. Matt runs
it, or approves the counts, before rollout.

### Not changed, worth a follow-up

Derivation for JWT users is odd and this PR leaves it alone: any non-empty
role claim derives `control_owner`, and an empty one derives `grc_engineer`.
So a JWT user with _no_ assignment out-ranks one assigned `viewer`, and an
assigned `auditor` keeps `control_owner`'s evidence write. Both predate #2239
and narrowing them would remove access, which needs its own call.
