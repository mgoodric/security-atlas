# 2273 — Last-admin follow-ups: decisions log

Issue: mgoodric/security-atlas#2273, follow-ups from the review of #2263
(#2261, the last-admin guard). Three changes:

1. A SCIM deprovision that leaves a tenant with no active admin is reported.
2. The group-role resolver takes the admin-roster lock only when it would
   revoke `admin`.
3. Every advisory lock moves onto a namespaced registry,
   `internal/db/advisorylock`.

## Detection-tier classification

- **detection_tier_actual:** none. No bug surfaced. Each new test was
  observed failing against the old behaviour and then passing:
  - Forcing the lock on every derivation fails
    `TestDerive_NoAdminRevokeSkipsRosterLock_2273` and
    `TestDerive_ConcurrentNonAdminLoginsDoNotSerialise_2273` (context
    deadline on the roster lock).
  - Disabling the alert fails `TestDeprovision_LastAdminAlertsSuperAdmins_2273`
    (all three paths) and `TestDeprovision_ConcurrentLastTwoAdminsAlertOnce_2273`.
  - The registry scan flagged a single-argument call before it passed.
- **detection_tier_target:** integration (locks, RLS, the notifier) and
  unit (the registry and source scan, `revokesAdmin`).

## Decisions

### D1 — the SCIM deprovision takes the roster lock, and is never refused

`watchAdmins` runs `users.LockAdmins` before the deprovision's writes, but
only when the request deactivates an active user. It is the same tenant-row
`FOR NO KEY UPDATE` lock the admin API and the reconciler take. Without it, two
concurrent deprovisions of the last two admins would each count the other
as still active, and neither would raise the alert.
`TestDeprovision_ConcurrentLastTwoAdminsAlertOnce_2273` queues both on the
lock and asserts exactly one alert.

After the writes, `AdminGuard.Check` recounts. `ErrLastAdmin` does not
refuse the change here, unlike in the admin API. It triggers the alert, and
the deprovision commits (#2261 D5 stands). A count that was already zero
does not alert: a 0 to 0 change is not a transition.

### D2 — the audit row goes to `scim_audit_log`

The deprovision is a SCIM action by a SCIM credential, and its own
`user.deprovision` / `user.delete` row is already in `scim_audit_log`. The
alert row sits next to it: the same actor credential, the deprovisioned user
as the target, and `detail` holding the counts before and after and the
number of super-admins notified. The `action` CHECK is replaced with a
superset that keeps all six existing values. The down migration restores the
six-value CHECK and fails while an alert row exists, so audit rows are never
deleted to make a rollback succeed.

### D3 — super-admins are notified through the notifications spine

The repo already has one way to alert a person: a row in `notifications`
(slice 029). The email, Slack and webhook digests deliver it, and the backup
failure alert uses it too. The alert uses that spine; it adds no new channel.

A super-admin's inbox is in their home tenant, which atlas_app's RLS
context in the SCIM transaction can neither read nor write. The insert
therefore goes through `notify_super_admins_last_admin_deprovisioned`, a
SECURITY DEFINER function (owner `atlas_migrate`, `search_path` pinned,
EXECUTE granted to `atlas_app` only). Three properties make that grant safe:

- it raises for any tenant but the caller's own RLS tenant;
- it writes nothing while that tenant still has an active admin, using the
  same predicate as `CountActiveTenantAdmins`;
- it skips a super-admin who already holds an unread alert for the same
  tenant.

So a caller cannot use the function to flood inboxes or to fabricate an
alert about a tenant that has an admin. `TestNotifySuperAdmins_Guards_2273`
covers the first two properties.

Rejected alternative: notifying after commit through a privileged pool
(the backup alerter's shape). That needs new pool wiring in `cmd/atlas`,
and a crash between commit and notify would lose the alert. In-transaction,
the alert commits with the deprovision or not at all.

The notification sits in the same transaction, so a failure to write it
fails the deprovision, just as a failure to write the deprovision's own audit row
already does. The function is a single insert; a failure there is a
database fault, not a policy refusal.

The payload carries ids only (`tenant_id`, `deprovisioned_user_id`). No
digest label was added for the type; the digests render it as "Other
notifications", as they already do for `backup.failure`.

### D4 — the reconciler locks only when the plan revokes `admin`

`revokesAdmin(target, current)` is true when the user holds a group-derived
`admin` that the new group set drops. Only then does the plan read the
admin count (`wouldStrandLastAdmin`), so only then is the lock needed.
Grants and non-admin revokes cannot remove an admin. Under the lock the
resolver re-reads the user's derived roles, so a concurrent reconcile of the
same user that committed while it waited is seen. The rule is the same for
the SCIM-group source, which calls the same resolver.

### D5 — advisory lock registry

All locks use `pg_advisory_xact_lock(int4 ns, int4 key)` (or the `_shared`
and session variants) with a namespace constant from
`internal/db/advisorylock`:

| ns  | Name                    | Was                                            | Key                     |
| --- | ----------------------- | ---------------------------------------------- | ----------------------- |
| 1   | bootstrap_first_install | `0x198198198198`                               | 0                       |
| 2   | super_admin_demote      | `0x142142142142`                               | 0                       |
| 3   | tenant_create_actor     | `0x0143` prefix + actor uuid bits              | `hashtext(actor uuid)`  |
| 4   | tenant_create_install   | `0x201520152015`                               | 0                       |
| 5   | theme_vocabulary        | `hashtextextended('theme_vocabulary:'‖tenant)` | `hashtext(tenant uuid)` |
| 6   | risk_hierarchy          | `hashtextextended('risk_hierarchy:'‖tenant)`   | `hashtext(tenant uuid)` |
| 7   | aggregation_key         | `hashtextextended(key)`                        | `hashtext(key)`         |
| 8   | integration_test_seed   | five `6502261335191781xxx` test session locks  | `TestKey*` constants    |

Distinct namespaces cannot block one another whatever their keys, and
Postgres keeps the two-int space apart from the bigint one
(`TestNamespacesDoNotCollide`). Within one namespace, a 32-bit `hashtext`
collision only over-serialises two unrelated tenants or keys; it never lets
two holders of the same key run together. `TestNoUnregisteredAdvisoryLocks`
scans `internal/`, `cmd/`, `pkg/`, `migrations/`, `scripts/`, `connectors/`
and `deploy/` for any advisory lock call. It fails on a one-argument form, an
unregistered literal namespace, or a parameterised namespace outside the
package, and it pins the namespace of each sqlc query. Test session locks
are included, so a test cannot reintroduce a bare bigint either.

The merged #2491 and #2474 add no advisory lock (#2491 tested for one and
found it unnecessary).

**Rollout note:** during a rolling deploy an old binary takes the bigint key
and a new binary takes the two-int key, so for that window the two do not
exclude each other. The v1 deployment is a single VM restarted in place, so
the window does not occur there. A multi-replica deploy should not overlap
versions across this change.
