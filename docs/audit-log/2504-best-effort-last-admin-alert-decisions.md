# 2504 — Best-effort last-admin alert: decisions log

Issue: mgoodric/security-atlas#2504, follow-ups from the review of #2499
(#2273). Two changes:

1. The super-admin notification and the `tenant_last_admin_deprovisioned`
   audit row no longer roll back the SCIM deprovision when they fail.
2. `notify_super_admins_last_admin_deprovisioned` validates its user
   argument (migration `20261243000000`).

## Detection-tier classification

- **detection_tier_actual:** manual_review. The review of #2499 found both
  gaps: a notify error rolled back the whole deprovision, and the SECURITY
  DEFINER function accepted any user id. Each new test was seen failing
  against the old behaviour before it passed:
  - With the notify error made fatal again,
    `TestDeprovision_NotifyFailureStillCommits_2504` fails on every path with
    `deprovision must commit despite the notify failure: ERROR: division by
zero`.
  - With the down migration applied, `TestNotifySuperAdmins_ValidatesUser_2504`
    accepts the null, unknown, active and other-tenant users.
- **detection_tier_target:** integration. The savepoint behaviour and the
  plpgsql check both need a real Postgres.

## Decisions

### D1: each alert step runs under its own SAVEPOINT

`alertIfLastAdmin` runs the notify call and the audit write each inside
`pgx.Tx.Begin`, which pgx implements as a SAVEPOINT on the deprovision
transaction. If either step errors, the code rolls back to that step's
savepoint and logs with `slog.ErrorContext`, and the deprovision goes on to
commit. The deprovision's other writes come earlier in the same transaction
and are not affected: the deactivation, the session and API-key revocation,
and the `user.deprovision` audit row. A notify failure is recorded on the
alert audit row as `detail.notify_error`. When notify succeeds, the row
carries `super_admins_notified` as before.

If the `ROLLBACK TO SAVEPOINT` itself fails, the outer transaction stays
aborted, `Commit` returns the error, and the deprovision fails as a whole.
No partly applied state commits.

The injected failure in the test is a real SQL error (`SELECT 1 / 0`), not a
Go error. A plain Go error would leave the transaction usable and would pass
even without a savepoint. The SQL error aborts the transaction, so only the
rollback to the savepoint can recover it.

### D2: a failed alert audit write does not block the deprovision

The dispatcher brief asked whether a failed `tenant_last_admin_deprovisioned`
audit write should fail closed. It does not. The failure is logged at ERROR,
and the deprovision commits.

- The audit trail for the security event still fails closed. The
  deactivation is recorded by the `user.deprovision` or `user.delete` row,
  which `Replace`, `Patch` and `Delete` write before the alert step and
  outside any savepoint. If that write fails, the deprovision rolls back
  exactly as it did before.
- The alert row records a side effect, the fact that super-admins were told.
  Making it fail closed would bring back the failure #2504 exists to remove.
  A departed person would keep access until the IdP retried, and a
  deterministic bug would fail every retry. A missing alert row is the
  smaller harm. Revoking access late is a security failure. A late or
  missing alert is an observability gap, and the ERROR log covers it.
- The tenant state the alert describes is still visible: the tenant has no
  active admin, and the `user.deprovision` row names who was removed.

### D3: what counts as an inactive user

The notifier accepts `p_deprovisioned_user` only when it is non-null, exists,
belongs to `p_tenant`, and is inactive. Inactive means
`NOT (status = 'active' AND active)`, the exact inverse of the active-admin
predicate the same function already uses, so every user the admin count
excludes is accepted. SCIM deprovision sets `active = false`. Any other path
that sets `status` away from `'active'` is accepted too. The check runs after
the tenant check, so a caller cannot use it to probe whether a user exists in
another tenant. The refusal uses SQLSTATE `22023` (invalid_parameter_value).
The tenant refusal keeps `42501`.

The function stays `SECURITY DEFINER` with `SET search_path = public,
pg_temp`. `TestNotifySuperAdmins_ValidatesUser_2504` asserts that pin from
`pg_proc.proconfig`.

### D4: the test seam is an unexported field

`Store.notify` is nil in production, and nil means the SQL function is
called. Only `export_test.go` (`WithNotifierForTest`) sets it, so the
production API does not change.
