# 2514 — SCIM last-admin alert: notify_error stores a SQLSTATE: decisions log

Issue: mgoodric/security-atlas#2514, a nit from the review of #2506 (#2504).
The `notify_error` key on the `tenant_last_admin_deprovisioned` audit row
held the raw `err.Error()`. Tenant admins can read that row, and a
non-Postgres error (connection refused, timeout) can name hosts, ports and
pool settings.

## Detection-tier classification

- **detection_tier_actual:** manual_review. The review of #2506 found it.
- **detection_tier_target:** unit. `notifyErrorDetail` is a pure function;
  `TestNotifyErrorDetail` pins it without a database.

Both new assertions were seen failing first:

- `TestNotifyErrorDetail` did not compile before `notifyErrorDetail` existed.
- `TestDeprovision_NotifyFailureStillCommits_2504`, tightened to require
  exactly `SQLSTATE 22012`, failed on the patch, replace and delete paths
  against the old `err.Error()` value.

The review also asked whether the `delete` subtest of
`TestDeprovision_NotifyFailureStillCommits_2504` was red before #2506. With
the notify error made fatal again (the pre-#2506 behaviour, keeping the test
hook), all three subtests fail with `deprovision must commit despite the
notify failure: ERROR: division by zero (SQLSTATE 22012)`, `delete` included.

## Decisions

### D1: a fixed text for non-Postgres errors, not a capped message

The issue allowed either a SQLSTATE or a message capped at 200 characters. A
Postgres error is stored as `SQLSTATE <code>`. Anything else gets the fixed
`notify failed (not a database error); see the server log`. A capped message
would still carry the start of a pgx connect error, which is where the host
and user appear, so capping alone does not close the leak. The slog line
beside it keeps `err` in full, so nothing is lost for the operator.

### D2: same key, no migration

`notify_error` keeps its name and stays a string, so readers of the audit
row see the same shape with less in it. Rows written before this change keep
their old text; they are audit history and are not rewritten.
