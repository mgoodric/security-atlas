# 2261 — Refuse removing a tenant's last active admin: decisions log

Issue: mgoodric/security-atlas#2261. The admin API refuses, with 409, any
change that would leave a tenant with zero active `admin` holders. The guard
lives in `internal/auth/users/lastadmin.go` (`LockAdmins` + `AdminGuard.Check`).

## Detection-tier classification

- **detection_tier_actual:** integration. Removing the roster lock makes
  `TestRevoke_ConcurrentLastTwoAdminsLeaveOne_2261` fail (both revokes return
  204, the tenant is left with 0 admins); with the lock it passes.
- **detection_tier_target:** integration.

## Decisions

### D1 — the lock is the tenant row, `FOR NO KEY UPDATE`

Every guarded path runs `SELECT 1 FROM tenants WHERE id = $1 FOR NO KEY
UPDATE` before its writes. `atlas_app` holds UPDATE on `tenants` and the RLS
policy admits its own row, so the lock works on both the RLS pool and the
BYPASSRLS auth pool. `NO KEY UPDATE` rather than `UPDATE` because `FOR
UPDATE` conflicts with the `FOR KEY SHARE` every foreign-key check on a
tenant-scoped insert takes; `NO KEY UPDATE` conflicts only with itself and
tenant updates. Under READ COMMITTED the count after the lock sees every admin
change committed before the lock was granted.

The lock fails closed: the statement returns the row, and when no row comes
back (an unknown tenant id, or RLS hiding it) `LockAdmins` returns
`ErrTenantNotLockable` and the handler answers 500 rather than running the
change unlocked (`TestRevoke_FailsClosedWithoutTenantRow_2261`).

Locking the admin `user_roles` rows instead was rejected: a disable changes
`users.status`, not `user_roles`, and a re-check of joined rows under
EvalPlanQual is subtle to get right. One row per tenant is a plain mutex.

The super-admin guard (`adminsuperadmins.Demote`) uses a fixed
`pg_advisory_xact_lock(0x142142142142)` because `atlas_app` has no UPDATE on
`super_admins`. Same shape (serialise, then count), different primitive. It
is platform-global, so a single fixed key is right there; a tenant guard
needs a per-tenant lock, which the tenant row gives without a key scheme.

### D2 — count after the writes, refuse on "had one, now none"

`Check` recounts after the path's own writes and refuses only when the tenant
had at least one active admin and now has none. One rule covers revoke,
revoke + disable, and role replacement without per-path arithmetic. A tenant
that already had no active admin is not made worse, so it passes. The check
runs before the audit insert and the transaction rolls back, so a refused
attempt writes nothing.

### D3 — "active" is `status = 'active' AND active`, by bare user id

Matches `users.SignInAllowed`. `user_roles.user_id` is TEXT; a `user:<uuid>`
principal is read as its bare uuid (the `identity.UserID` rule, #2186). A role
row that names no users row (an API key, `bootstrap-admin`) is not counted.

### D4 — paths guarded

| Path                                                                              | Guard                                                                   |
| --------------------------------------------------------------------------------- | ----------------------------------------------------------------------- |
| `POST /v1/admin/users/revoke` (incl. `remove_membership`, the API's user disable) | lock + check, 409                                                       |
| `PATCH /v1/admin/users/{id}/roles`                                                | lock + check, 409                                                       |
| Group-role reconciler (`grouprole.Resolver.Derive`, SCIM groups + OIDC login)     | lock; its existing suppress-the-revoke guard now reads the active count |

There is no hard user delete anywhere in the code.

### D5 — SCIM user deactivation is NOT guarded

`PUT/PATCH /scim/v2/Users/{id}` with `active=false` and `DELETE
/scim/v2/Users/{id}` are left unguarded. They are the IdP telling us a person
has left. Refusing them would keep a departed person's admin access alive to
avoid an empty roster, which is the worse failure for a security product.
After such a deprovision a super_admin can still assign an admin. Revisit if
operators would rather have the IdP call fail.

### D6 — reconciler suppression stays conservative

`wouldStrandLastAdmin` still treats `count <= 1` as stranding. With an active
count, a disabled user who holds a group-derived admin while exactly one other
active admin exists keeps the role (a suppressed revoke in the result). The
user cannot sign in, so this is harmless, and it keeps the pure plan
unchanged.
